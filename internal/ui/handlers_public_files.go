package ui

// Отдача опубликованных вложений (план 127): GET /pub/{token} без
// аутентификации. Токен непредсказуем и отзывается — см. storage/public_files.go.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// inlineSafeType сообщает, можно ли показывать такой тип прямо в браузере.
//
// text/html и svg на своём домене = XSS с доступом к cookie админки: страница
// откроется в origin платформы. Такие файлы отдаются вложением с нейтральным
// типом, а не inline.
func inlineSafeType(mime string) bool {
	m := strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	switch {
	case m == "image/svg+xml": // векторная картинка со скриптом внутри
		return false
	case strings.HasPrefix(m, "image/"), strings.HasPrefix(m, "video/"), strings.HasPrefix(m, "audio/"):
		return true
	case m == "application/pdf", m == "text/plain":
		return true
	}
	return false
}

// opPublicFileServe ограничивает одновременные отдачи /pub: поверхность
// анонимная, а блоб из стора без Seek читается в память целиком — без предела
// N параллельных медленных клиентов держали бы N буферов.
//
// Лишние запросы ЖДУТ очереди, а не получают отказ сразу: страница с двумя
// десятками картинок и несколькими посетителями штатно даёт больше сотни
// одновременных запросов, и отказ по превышению выглядел бы как сломанный
// сайт. Память при этом ограничена так же — ожидающий буфера не держит.
// Отказ остаётся только для того, кто не дождался за publicFileServeWait.
const (
	opPublicFileServe          = "public_file.serve"
	publicFileServeConcurrency = 64
	publicFileServeWait        = 10 * time.Second
)

// publicFileServe отдаёт файл по токену публикации. Любая неудача — 404:
// существование вложения тоже информация.
func (s *Server) publicFileServe(w http.ResponseWriter, r *http.Request) {
	// Публичная отдача — та же поверхность конфигурации наружу, что и /hs/*,
	// поэтому подчиняется предохранителю сети (план 62).
	if !s.netEnabled(r.Context()) {
		http.Error(w, ErrNetworkLocked.Error(), http.StatusServiceUnavailable)
		return
	}
	if s.ops == nil {
		s.ops = newOperationLimiter()
	}
	waitCtx, cancelWait := context.WithTimeout(r.Context(), publicFileServeWait)
	release, ok := s.ops.acquire(waitCtx, opPublicFileServe, publicFileServeConcurrency)
	cancelWait()
	if !ok {
		// Сюда попадают двое: не дождавшийся за отведённое время и уже
		// отключившийся клиент. Второму ответ некуда девать, но и вреда нет.
		w.Header().Set("Retry-After", "1")
		http.Error(w, "слишком много одновременных запросов", http.StatusServiceUnavailable)
		return
	}
	defer release()
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	if token == "" {
		http.NotFound(w, r)
		return
	}
	pf, err := s.store.PublicFileByToken(r.Context(), token)
	if err != nil || pf == nil || pf.Expired(time.Now()) {
		http.NotFound(w, r)
		return
	}

	h := w.Header()
	// Слабый ETag зависит только от capability-токена: содержимое за живым
	// токеном неизменно. Ставим валидаторы и обрабатываем совпадение ДО открытия
	// вложения/S3-блоба — иначе обязательная ревалидация сначала скачивала бы
	// весь объект, а уже затем возвращала короткий 304.
	etag := `W/"` + pf.Token + `"`
	h.Set("ETag", etag)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	// Тело можно хранить и повторно использовать после 304, но перед КАЖДЫМ
	// использованием кэш обязан свериться с origin. Иначе fresh max-age позволил
	// бы браузеру или shared proxy отдавать файл после удаления capability-токена
	// из БД. Это условие одновременно обеспечивает немедленный отзыв и окончание
	// ДействуетДо без попытки очищать неподконтрольные onebase внешние кэши.
	h.Set("Cache-Control", "public, no-cache, max-age=0, must-revalidate")
	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	var (
		content  io.ReadSeeker
		mimeType string
		name     = pf.Filename
		modified time.Time
	)
	if pf.IsBlob() {
		// Картинка из поля image.
		blob, rc, berr := s.store.OpenBlob(r.Context(), pf.BlobID)
		if berr != nil {
			http.NotFound(w, r)
			return
		}
		if rs, seekable := rc.(io.ReadSeeker); seekable {
			// Файловый стор отдаёт *os.File: ServeContent работает по Seek,
			// копия в память не нужна — блоб может весить до maxFileSizeBytes.
			defer closeRead("публичная картинка", rc)
			content = rs
		} else {
			// Стор без Seek (S3-поток): читаем в память — ServeContent требует
			// Seeker ради Range. Одновременные чтения ограничены лимитером выше.
			data, rerr := io.ReadAll(rc)
			closeRead("публичная картинка", rc)
			if rerr != nil {
				http.NotFound(w, r)
				return
			}
			content = bytes.NewReader(data)
		}
		mimeType = blob.Mime
		if name == "" {
			name = "image"
		}
	} else {
		f, att, aerr := s.store.OpenAttachment(r.Context(), pf.AttachmentID)
		if aerr != nil {
			http.NotFound(w, r)
			return
		}
		defer closeRead("публичный файл", f)
		content, mimeType, modified = f, att.MimeType, att.UploadedAt
		if name == "" {
			name = att.Filename
		}
	}

	if inlineSafeType(mimeType) {
		h.Set("Content-Type", mimeType)
		// Через dispositionHeader, а не strconv.Quote: сырой UTF-8 в
		// quoted-string браузеры читают как latin-1 (issue #46).
		h.Set("Content-Disposition", dispositionHeader("inline", name))
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", contentDisposition(name))
	}
	// ServeContent сам обрабатывает Range, If-Modified-Since и If-None-Match.
	http.ServeContent(w, r, name, modified, content)
}
