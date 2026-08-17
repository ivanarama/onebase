package ui

// Сжатие ответов HTTP-сервисов (план 128).
//
// Ответы /hs/* уходили без компрессии: на выгрузке обмена в JSON и на
// HTML-странице это троекратный проигрыш по трафику. Включается декларативно
// (`compress:` в services/<имя>.yaml), по умолчанию — только для анонимных
// сервисов: сжатие ответа, содержащего секрет вместе с отражённым вводом
// атакующего, выдаёт секрет по длине (BREACH), а у auth: none секретов нет по
// определению.

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"

	oblog "github.com/ivantit66/onebase/internal/logging"
)

// compressMinSize — порог включения gzip. На коротком ответе сжатие даёт
// отрицательную экономию и лишний CPU.
const compressMinSize = 1024

var gzipWriterPool = sync.Pool{
	New: func() any { return gzip.NewWriter(nil) },
}

// compressibleType сообщает, стоит ли сжимать такой Content-Type. Уже сжатые
// форматы (png, pdf, zip) трогать бессмысленно.
func compressibleType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "application/json", "application/xml", "application/javascript",
		"application/x-javascript", "image/svg+xml", "application/rss+xml",
		"application/atom+xml", "application/manifest+json":
		return true
	}
	return strings.HasPrefix(ct, "text/")
}

func clientAcceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(enc), "gzip") {
			continue
		}
		// «gzip;q=0» — явный отказ от кодирования.
		if strings.Contains(strings.ReplaceAll(strings.ToLower(params), " ", ""), "q=0") &&
			!strings.Contains(strings.ToLower(params), "q=0.") {
			return false
		}
		return true
	}
	return false
}

// gzipResponseWriter решает вопрос «сжимать ли» уже по факту записи: до порога
// данные копятся в буфере, а Content-Type к этому моменту установлен
// обработчиком.
type gzipResponseWriter struct {
	http.ResponseWriter
	buf      []byte
	gz       *gzip.Writer
	decided  bool
	compress bool
	status   int
	headersOut bool
}

func newGzipResponseWriter(w http.ResponseWriter) *gzipResponseWriter {
	return &gzipResponseWriter{ResponseWriter: w, status: http.StatusOK}
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	// Заголовок откладываем: решение о Content-Encoding принимается после того,
	// как станет известен объём и тип тела.
	w.status = status
}

func (w *gzipResponseWriter) Write(p []byte) (int, error) {
	if !w.decided {
		w.buf = append(w.buf, p...)
		if len(w.buf) < compressMinSize {
			return len(p), nil
		}
		w.decide(true)
		return len(p), w.flushBuffer()
	}
	if w.compress {
		return w.gz.Write(p)
	}
	w.writeHeaders()
	return w.ResponseWriter.Write(p) //nolint:gosec // G705: тело формирует обработчик сервиса, тип ответа он же и объявляет
}

// decide фиксирует режим. enough=false означает «тело меньше порога» — тогда
// сжатие не включается независимо от типа.
func (w *gzipResponseWriter) decide(enough bool) {
	w.decided = true
	w.compress = enough && compressibleType(w.Header().Get("Content-Type"))
	if !w.compress {
		return
	}
	h := w.Header()
	h.Set("Content-Encoding", "gzip")
	// Без Vary промежуточный кэш (в том числе наш, план 126) отдаст сжатое тело
	// клиенту, который gzip не принимает.
	h.Add("Vary", "Accept-Encoding")
	// Длина несжатого тела к сжатому не относится.
	h.Del("Content-Length")
	gz, _ := gzipWriterPool.Get().(*gzip.Writer)
	gz.Reset(w.ResponseWriter)
	w.gz = gz
}

func (w *gzipResponseWriter) writeHeaders() {
	if w.headersOut {
		return
	}
	w.headersOut = true
	w.ResponseWriter.WriteHeader(w.status)
}

func (w *gzipResponseWriter) flushBuffer() error {
	w.writeHeaders()
	buf := w.buf
	w.buf = nil
	if len(buf) == 0 {
		return nil
	}
	if w.compress {
		_, err := w.gz.Write(buf)
		return err
	}
	_, err := w.ResponseWriter.Write(buf) //nolint:gosec // G705: см. Write
	return err
}

// Close дописывает остаток и возвращает gzip.Writer в пул. Вызывать
// обязательно (defer), иначе тело короткого ответа не уйдёт клиенту.
func (w *gzipResponseWriter) Close() {
	if !w.decided {
		w.decide(false)
	}
	if err := w.flushBuffer(); err != nil {
		oblog.Component("http").Warn("сжатие ответа: не удалось записать тело", "error", err)
	}
	if w.gz != nil {
		if err := w.gz.Close(); err != nil {
			oblog.Component("http").Warn("сжатие ответа: не удалось закрыть поток", "error", err)
		}
		gzipWriterPool.Put(w.gz)
		w.gz = nil
	}
}

// Flush поддерживает потоковую отдачу. Обёртка обязана пробрасывать Flusher:
// без этого «живые» ответы (SSE) молча перестают доходить до клиента — эта
// грабля уже стоила инцидента с metrics-обёрткой.
func (w *gzipResponseWriter) Flush() {
	if !w.decided {
		// Обработчик просит отдать то, что есть: значит порог не показатель,
		// решаем по типу и объёму уже накопленного.
		w.decide(len(w.buf) >= compressMinSize)
		if err := w.flushBuffer(); err != nil {
			oblog.Component("http").Warn("сжатие ответа: не удалось записать тело", "error", err)
			return
		}
	}
	if w.gz != nil {
		if err := w.gz.Flush(); err != nil {
			oblog.Component("http").Warn("сжатие ответа: не удалось сбросить поток", "error", err)
			return
		}
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
