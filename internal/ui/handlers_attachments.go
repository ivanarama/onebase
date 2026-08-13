package ui

// HTTP-обработчики вложений к объектам.
// Выделено из handlers.go (план 55, этап 1) — перенос as-is.

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

// Нормализация имени вложения (защита от path-traversal/XSS/DoS) вынесена в
// storage.SanitizeAttachmentName — единый источник для UI- и REST-пути загрузки.

// attachmentsList returns JSON list of attachments for a record.
func (s *Server) attachmentsList(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	if !s.requireOwnerRow(w, r, string(entity.Kind), entity.Name, "read", id) {
		return
	}

	atts, err := s.store.ListAttachments(r.Context(), string(entity.Kind), entity.Name, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if atts == nil {
		atts = []storage.Attachment{}
	}

	w.Header().Set("Content-Type", "application/json")
	respondJSONTo(w, atts)
}

// attachmentUpload handles file upload for a record.
func (s *Server) attachmentUpload(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	if !s.requireOwnerRow(w, r, string(entity.Kind), entity.Name, "write", id) {
		return
	}

	maxSize := s.limitMultipartRequest(w, r)

	lang := s.resolveLang(r)
	if err := parseBoundedForm(r, 32<<20); err != nil {
		http.Error(w, s.tr(lang, "Ошибка разбора формы")+": "+s.errText(r, err), uploadErrorStatus(err))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, s.tr(lang, "Нет файла в форме"), 400)
		return
	}
	defer closeRead("загруженный файл", file)

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	uploadedBy := ""
	if u := auth.UserFromContext(r.Context()); u != nil {
		uploadedBy = u.Login
	}

	filename := storage.SanitizeAttachmentName(header.Filename)
	if !storage.AttachmentExtAllowed(s.allowedAttachmentTypes, filename) {
		http.Error(w, s.tr(lang, "Недопустимый тип файла"), http.StatusUnsupportedMediaType)
		return
	}

	_, err = s.store.UploadAttachment(r.Context(), string(entity.Kind), entity.Name, id,
		filename, mimeType, uploadedBy, file, maxSize)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrAttachmentTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, s.errText(r, err), status)
		return
	}

	http.Redirect(w, r, r.Referer(), http.StatusSeeOther)
}

// attachmentDownload serves a file attachment for download.
func (s *Server) attachmentDownload(w http.ResponseWriter, r *http.Request) {
	aid, err := uuid.Parse(chi.URLParam(r, "aid"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	f, att, err := s.store.OpenAttachment(r.Context(), aid)
	if err != nil {
		http.Error(w, s.tr(s.resolveLang(r), "Файл не найден"), 404)
		return
	}
	defer closeRead("загруженный файл", f)

	// Авторизация (защита от IDOR): по умолчанию требуется право ЧТЕНИЯ родителя
	// — как в REST v2 и DSL. Право «запись» БОЛЬШЕ не даёт скачивание (SEC-02,
	// #777): раньше «read OR write» позволяло роли «только запись» получить
	// содержимое любого вложения по UUID. Узкое исключение — предпросмотр
	// пользователем ТОЛЬКО ЧТО ЗАГРУЖЕННОГО им файла (см. uploaderPreviewAllowed).
	if !s.rowAllowsOwnerID(r, att.OwnerKind, att.OwnerName, "read", att.OwnerID) &&
		!s.uploaderPreviewAllowed(r, att) {
		http.Error(w, s.tr(s.resolveLang(r), "Нет доступа"), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", att.MimeType)
	w.Header().Set("Content-Disposition", contentDisposition(att.Filename))
	http.ServeContent(w, r, att.Filename, att.UploadedAt, f)
}

// attachmentPreviewWindow — окно, в течение которого загрузивший файл может
// скачать его сразу после загрузки, даже если его read-фильтр (RLS) ещё не
// покрывает только что созданную строку-владельца.
const attachmentPreviewWindow = 10 * time.Minute

// uploaderPreviewAllowed разрешает скачивание вложения его загрузчику в течение
// короткого окна после загрузки. Привязка к ЛИЧНОСТИ загрузчика и свежести
// файла, а не к праву «запись», — поэтому роль «только запись» не получает
// доступ к чужим или старым вложениям (SEC-02, #777).
func (s *Server) uploaderPreviewAllowed(r *http.Request, att *storage.Attachment) bool {
	u := auth.UserFromContext(r.Context())
	if u == nil || att == nil || att.UploadedBy == "" || u.Login != att.UploadedBy {
		return false
	}
	// Пользователь мог потерять write после загрузки; одна строка uploaded_by не
	// должна сохранять ему доступ. Отрицательный возраст (время из будущего из-за
	// рассинхронизации часов/повреждённых данных) также не считается «свежим».
	if !s.rowAllowsOwnerID(r, att.OwnerKind, att.OwnerName, "write", att.OwnerID) {
		return false
	}
	age := time.Since(att.UploadedAt)
	return age >= 0 && age <= attachmentPreviewWindow
}

// attachmentDelete removes a file attachment.
func (s *Server) attachmentDelete(w http.ResponseWriter, r *http.Request) {
	aid, err := uuid.Parse(chi.URLParam(r, "aid"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	// Авторизация (защита от IDOR): удалять вложение может только тот, у кого есть
	// право записи на сущность-владельца. Метаданные грузим до удаления.
	att, err := s.store.GetAttachment(r.Context(), aid)
	if err != nil {
		http.Error(w, s.tr(s.resolveLang(r), "Файл не найден"), 404)
		return
	}
	if !s.rowAllowsOwnerID(r, att.OwnerKind, att.OwnerName, "write", att.OwnerID) {
		http.Error(w, s.tr(s.resolveLang(r), "Нет доступа"), http.StatusForbidden)
		return
	}

	if err := s.store.DeleteAttachment(r.Context(), aid); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, r.Referer(), http.StatusSeeOther)
}
