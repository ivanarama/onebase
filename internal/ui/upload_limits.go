package ui

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/richtext"
)

const (
	defaultUIUploadBytes = int64(50 << 20)
	uiMultipartOverhead  = int64(2 << 20)
)

var errUploadTooLarge = errors.New("uploaded file is too large")

func (s *Server) effectiveUploadLimit() int64 {
	if s.maxFileSizeBytes > 0 {
		return s.maxFileSizeBytes
	}
	return defaultUIUploadBytes
}

func (s *Server) limitMultipartRequest(w http.ResponseWriter, r *http.Request) int64 {
	limit := s.effectiveUploadLimit()
	r.Body = http.MaxBytesReader(w, r.Body, limit+uiMultipartOverhead)
	return limit
}

// parseBoundedForm avoids ParseMultipartForm's misleading ErrNotMultipart for
// ordinary urlencoded forms while retaining its bounded temp-file behavior for
// actual multipart requests.
func parseBoundedForm(r *http.Request, maxMemory int64) error {
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if strings.EqualFold(contentType, "multipart/form-data") {
		return r.ParseMultipartForm(maxMemory)
	}
	return r.ParseForm()
}

func readUploadedBytes(file multipart.File, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errUploadTooLarge
	}
	return data, nil
}

func uploadErrorStatus(err error) int {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) || errors.Is(err, errUploadTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func (s *Server) uploadTooLargeText(lang string, limit int64) string {
	return fmt.Sprintf(s.tr(lang, "файл превышает максимальный размер %d МБ"), limit>>20)
}

// defaultFormMemoryBytes — сколько памяти отводится под разбор обычной формы
// (не загрузки файла). Админские и справочные формы состоят из коротких полей,
// поэтому мегабайта достаточно; превышение вернёт 413, а не съест память.
const defaultFormMemoryBytes = int64(1 << 20)

// richTextFieldBodyBytes — сколько тела формы отводится на ОДИН richtext-реквизит.
//
// richtext.MaxBytes считается по СЫРОМУ значению поля, а по проводу оно идёт
// urlencoded: base64-картинки раздуваются на «+»→%2B, «/»→%2F, «=»→%3D, кириллица
// — до шести байт на символ. Тройной запас покрывает худший случай, когда
// экранируется каждый байт значения. Меньший коэффициент оставил бы окно, в
// котором значение проходит по richtext.MaxBytes, но не проходит по размеру тела,
// — то есть ровно тот разрыв, из-за которого заведён #629.
const richTextFieldBodyBytes = richtext.MaxBytes * 3

// Инвариант на этапе компиляции: предел тела формы с richtext обязан быть больше
// самого richtext.MaxBytes, иначе checkRichTextLimits недостижима в принципе.
// Отрицательная разница переполнит uint и уронит сборку.
const _ = uint(richTextFieldBodyBytes - richtext.MaxBytes)

// richTextFieldCount — сколько richtext-реквизитов у сущности (шапка и ТЧ:
// значение любого из них приезжает в теле той же формы).
func richTextFieldCount(entity *metadata.Entity) int {
	if entity == nil {
		return 0
	}
	n := 0
	for _, f := range entity.Fields {
		if metadata.IsRichText(f.Type) {
			n++
		}
	}
	for _, tp := range entity.TableParts {
		for _, f := range tp.Fields {
			if metadata.IsRichText(f.Type) {
				n++
			}
		}
	}
	return n
}

// formBodyLimit — предел тела формы записи объекта. Обычная форма состоит из
// коротких полей, ей хватает мегабайта; форме с richtext нужен запас, выведенный
// из richtext.MaxBytes, чтобы два предела не разъезжались при правке одного из
// них. Поднимать общий defaultFormMemoryBytes нельзя: его делят около сорока
// обработчиков, включая формы входа и 2FA, где мегабайт осмыслен.
func formBodyLimit(entity *metadata.Entity) int64 {
	n := richTextFieldCount(entity)
	if n == 0 {
		return defaultFormMemoryBytes
	}
	return defaultFormMemoryBytes + int64(n)*richTextFieldBodyBytes
}

// entityFormBodyLimit — предел тела для запроса записи объекта. Возвращает
// число, а не оборачивает r.Body сам: присваивание r.Body должно остаться в теле
// обработчика, иначе gosec (G120) перестаёт видеть предел и помечает каждый
// r.FormValue в функции.
//
// Пределы не композируются — вложенный MaxBytesReader всегда связывает по
// МЕНЬШЕМУ, поэтому для multipart (на том же маршруте едут вложения) берём
// максимум, а не ставим второй предел поверх первого.
func (s *Server) entityFormBodyLimit(r *http.Request, entity *metadata.Entity) int64 {
	limit := formBodyLimit(entity)
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); strings.EqualFold(ct, "multipart/form-data") {
		if up := s.effectiveUploadLimit() + uiMultipartOverhead; up > limit {
			limit = up
		}
	}
	return limit
}

// formBodyError переводит «http: request body too large» на человеческий. Для
// формы с richtext называется именно тот предел, в который пользователь упёрся
// по смыслу: сырое сообщение про тело запроса ничего ему не объясняет.
func formBodyError(err error, entity *metadata.Entity) error {
	var maxErr *http.MaxBytesError
	if !errors.As(err, &maxErr) {
		return err
	}
	if richTextFieldCount(entity) > 0 {
		return i18nerr.Errorf("превышен размер данных формы: форматированный текст с картинками не должен превышать %d МБ в одном поле", int64(richtext.MaxBytes)>>20)
	}
	return i18nerr.Errorf("превышен размер данных формы (не более %d МБ)", defaultFormMemoryBytes>>20)
}
