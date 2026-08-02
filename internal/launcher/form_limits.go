package launcher

import (
	"errors"
	"mime"
	"net/http"
	"strings"
)

// Разбор формы запроса в обработчиках лаунчера.
//
// Игнорировать ошибку r.ParseForm() нельзя, и дело не в аккуратности. При сбое
// разбора тела выполнение продолжается, а все FormValue возвращают пустые
// строки — обработчик принимает пустоту за осознанный ввод пользователя.
//
// Причём «всё пустое» — не единственный сценарий. ParseForm разбирает и тело, и
// строку запроса, а ошибку тела возвращает уже после того, как значения из query
// попали в r.Form. То есть при битом теле часть полей приходит из URL, а
// остальные оказываются пустыми — и обработчик видит правдоподобную, но
// наполовину стёртую форму. В handler.update это означает: имя берётся из query
// и проходит проверку, а Path, DBPath и Port обнуляются и сохраняются в
// хранилище — регистрация базы теряет путь к конфигурации и к файлу БД.
//
// Разбор здесь ограниченный: голый ParseForm читает тело целиком, без предела
// (gosec помечает это как G120). MaxBytesReader закрывает и это, и даёт
// обработчикам конфигуратора предел размера тела, которого у них не было.

const (
	// maxFormBody — предел тела обычной формы лаунчера. Формы конфигуратора
	// несут исходники модулей, отчётов и макетов, поэтому предел заметно выше
	// административного: 8 МиБ — это заведомо больше любого реального модуля,
	// но уже не «сколько пришлёт клиент».
	maxFormBody = int64(8 << 20)

	// formMemoryBytes — сколько multipart-формы держат в памяти; остаток
	// уходит во временный файл силами ParseMultipartForm.
	formMemoryBytes = int64(1 << 20)
)

// parseBoundedForm разбирает форму, не подменяя urlencoded-путь на multipart:
// ParseMultipartForm на обычной форме возвращает сбивающий с толку
// ErrNotMultipart, а для настоящего multipart нужно именно его ограниченное
// поведение с временным файлом.
func parseBoundedForm(r *http.Request, maxMemory int64) error {
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if strings.EqualFold(contentType, "multipart/form-data") {
		return r.ParseMultipartForm(maxMemory)
	}
	return r.ParseForm()
}

// parseFormLimited ограничивает тело запроса и разбирает форму. Возвращает
// ошибку — вызывающий обязан прекратить обработку, а не работать с пустыми
// FormValue.
func parseFormLimited(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	return parseBoundedForm(r, formMemoryBytes)
}

// formErrorStatus отличает превышение размера (413) от прочих сбоев разбора (400).
func formErrorStatus(err error) int {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

// failForm отвечает на нечитаемую форму и сообщает вызывающему, что обработку
// пора прекратить. Текст намеренно служебный и без перевода — как соседние
// http.Error в этом пакете: до пользователя такой ответ доходит только при
// сломанном или подделанном запросе, а не при обычной работе.
func failForm(w http.ResponseWriter, r *http.Request) bool {
	if err := parseFormLimited(w, r); err != nil {
		http.Error(w, "bad form: "+err.Error(), formErrorStatus(err))
		return true
	}
	return false
}
