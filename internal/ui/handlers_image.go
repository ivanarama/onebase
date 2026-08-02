package ui

// HTTP-обработчики поля типа image: загрузка картинки в blob-хранилище и
// отдача по UUID. Поле сущности хранит только ссылку (UUID); сам бинарник
// лежит на диске или в БД (см. storage blob backend, режим ui.file_storage).

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// imageUpload принимает картинку (multipart-поле "file") в контексте сущности,
// сохраняет её в blob-хранилище и возвращает JSON {"ref":"<uuid>"}. Ссылку
// форма кладёт в скрытое поле и сохраняет вместе с записью (поле типа image).
func (s *Server) imageUpload(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "write") {
		return
	}

	maxSize := s.limitMultipartRequest(w, r)

	lang := s.resolveLang(r)
	if err := parseBoundedForm(r, 32<<20); err != nil {
		http.Error(w, s.tr(lang, "Ошибка разбора формы")+": "+s.errText(r, err), uploadErrorStatus(err))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, s.tr(lang, "Нет файла в форме"), 400)
		return
	}
	defer closeRead("загруженную картинку", file)

	// Тип определяем по СОДЕРЖИМОМУ файла, а не по Content-Type формы (он
	// подделывается): читаем первые 512 байт для http.DetectContentType и
	// «возвращаем» их в поток через MultiReader. Это отсекает обычный SVG/HTML
	// (он распознаётся как text/*), но НЕ является единственным барьером:
	// GIF-полиглот (правильная сигнатура GIF89a + произвольный «хвост») всё ещё
	// классифицируется как image/gif. Фактическую защиту от XSS даёт сторона
	// отдачи imageServe — nosniff + честный Content-Type + sandbox-CSP, поэтому
	// эти заголовки трогать нельзя.
	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	head = head[:n]
	mimeType, ok := allowedImageMime(head)
	if !ok {
		http.Error(w, s.tr(lang, "Можно загрузить только изображение"), 400)
		return
	}
	body := io.MultiReader(bytes.NewReader(head), file)

	// Владелец бинарника = сущность, в контексте которой идёт загрузка. imageServe
	// по нему проверяет право чтения при отдаче (защита от IDOR).
	owner := storage.BlobOwner{Kind: string(entity.Kind), Entity: entity.Name}
	b, err := s.store.PutBlob(r.Context(), mimeType, body, maxSize, owner)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrBlobTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, s.errText(r, err), status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	respondJSONTo(w, map[string]string{"ref": b.ID.String()})
}

// imageServe отдаёт бинарник по UUID (значение поля image). Бинарник
// адресуется неизменяемым UUID, поэтому помечается долгоживущим кэшем.
func (s *Server) imageServe(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	b, rc, err := s.store.OpenBlob(r.Context(), id)
	if err != nil {
		http.Error(w, s.tr(s.resolveLang(r), "Файл не найден"), 404)
		return
	}
	defer closeRead("загруженную картинку", rc)

	// Авторизация (защита от IDOR): если у блоба есть владелец-сущность, отдаём
	// только тем, у кого есть право чтения видимой строки (или записи — чтобы
	// превью сразу после загрузки работало у загрузчика). can() возвращает true
	// для nil-пользователя, поэтому в открытом деплое без пользователей доступ
	// остаётся свободным.
	// Легаси-блобы без владельца уже защищены auth-middleware (аноним до сюда
	// не доходит, если пользователи заведены) — отдельная проверка не нужна.
	if b.OwnerEntity != "" {
		if !s.blobAllowed(r, b) {
			http.Error(w, s.tr(s.resolveLang(r), "Нет доступа"), http.StatusForbidden)
			return
		}
	}

	// Content-Type отдаём как есть только для растровых типов; всё прочее
	// (например text/html, сохранённый через СохранитьКартинку с произвольным
	// mime) выдаём как application/octet-stream — вместе с nosniff и sandbox это
	// исключает интерпретацию как HTML при прямом открытии /image/{id}.
	if strings.HasPrefix(b.Mime, "image/") {
		w.Header().Set("Content-Type", b.Mime)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	if b.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(b.Size, 10))
	}
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	// nosniff ставим явно (идемпотентно с глобальным middleware): imageServe —
	// единственная точка отдачи произвольного blob по UUID, и комментарии выше
	// опираются именно на этот заголовок, поэтому он не должен зависеть от того,
	// подключён ли middleware на конкретном маршруте.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Бинарник отдаётся inline и в режиме sandbox: даже если в хранилище есть
	// SVG со скриптом (загруженный до ужесточения проверки типа), при прямом
	// открытии /image/{id} он будет инертен. На отрисовку через <img> не влияет.
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	// Ответ уже начат — статус не поменять. Картинка, дописанная наполовину,
	// видна пользователю сразу, а причина обрыва почти всегда внешняя.
	if _, err := io.Copy(w, rc); err != nil {
		uiLog().Debug("картинка не дописана в ответ", "err", err)
	}
}

func (s *Server) blobAllowed(r *http.Request, b storage.Blob) bool {
	entity := s.ownerEntity(b.OwnerKind, b.OwnerEntity)
	if entity == nil {
		return s.can(r, b.OwnerKind, b.OwnerEntity, "read") || s.can(r, b.OwnerKind, b.OwnerEntity, "write")
	}
	if s.blobAllowedByRows(r.Context(), entity, "read", b.ID, false) {
		return true
	}
	if s.blobAllowedByRows(r.Context(), entity, "write", b.ID, true) {
		return true
	}
	return !s.blobReferenced(r.Context(), entity, b.ID) && s.can(r, b.OwnerKind, b.OwnerEntity, "write")
}

func (s *Server) blobAllowedByRows(ctx context.Context, entity *metadata.Entity, op string, id uuid.UUID, requireReference bool) bool {
	dec, err := s.rowDecision(ctx, entity, op)
	if err != nil || !dec.Allowed {
		return false
	}
	if dec.Unrestricted {
		if requireReference {
			return s.blobReferenced(ctx, entity, id)
		}
		return true
	}
	return s.blobReferencedWithPolicy(ctx, entity, id, dec.Predicate)
}

func (s *Server) blobReferenced(ctx context.Context, entity *metadata.Entity, id uuid.UUID) bool {
	return s.blobReferencedWithPolicy(ctx, entity, id, nil)
}

func (s *Server) blobReferencedWithPolicy(ctx context.Context, entity *metadata.Entity, id uuid.UUID, rowFilter *storage.Predicate) bool {
	if entity == nil {
		return false
	}
	for _, f := range entity.Fields {
		if !metadata.IsImage(f.Type) {
			continue
		}
		imageFilter := storage.Predicate{Field: f.Name, Op: "eq", Value: id.String()}
		filter := &imageFilter
		if rowFilter != nil {
			filter = &storage.Predicate{All: []storage.Predicate{imageFilter, *rowFilter}}
		}
		rows, err := s.store.List(ctx, entity.Name, entity, storage.ListParams{Limit: 1, RowFilter: filter, RowFilterEvaluated: true})
		if err == nil && len(rows) > 0 {
			return true
		}
	}
	return false
}

// allowedImageMime определяет тип картинки по её первым байтам (server-side, без
// доверия заголовку формы) — первая линия фильтра загрузки. Отсекает обычный SVG
// (распознаётся как text/xml) и HTML, но это НЕ гарантия безопасности: контент с
// валидной растровой сигнатурой и произвольным «хвостом» (GIF-полиглот) пройдёт.
// Защиту от XSS обеспечивает отдача imageServe (nosniff + sandbox-CSP), а не этот
// фильтр — см. TestImageServe_SecurityHeaders.
func allowedImageMime(head []byte) (string, bool) {
	mime := http.DetectContentType(head)
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = mime[:i]
	}
	return mime, strings.HasPrefix(mime, "image/")
}
