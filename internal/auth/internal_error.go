package auth

// Единая точка отказа «internal error» (#1053).
//
// Заявку завёл отчёт, в котором вход отвечал 500, а журнал сервера выглядел так:
//
//	msg="http request" method=POST uri="/login?return=/" status=500 bytes=15
//
// И всё. Ни строки о причине — ни у пользователя, ни у нас. Разбирать такое
// нечем: до этой правки ВСЕ двенадцать мест в пакете, отвечающих «internal
// error», молча проглатывали ошибку. Отказ, не назвавший причину, стоит дороже
// самого отказа: он превращает пятиминутную диагностику в переписку.
//
// Поэтому ответ и журнал разведены: пользователю — общая фраза (наружу нельзя
// отдавать ни SQL, ни имена таблиц, ни адрес провайдера SSO), администратору —
// строка уровня ERROR с операцией, ошибкой и маршрутом.

import (
	"net/http"
)

// logInternalError пишет причину отказа в журнал.
//
// op — короткое имя операции («создание сессии», «включение 2FA»): по нему
// администратор понимает, на каком шаге входа всё встало, не читая код.
func logInternalError(r *http.Request, op string, err error) {
	attrs := []any{"операция", op, "err", err}
	if r != nil {
		attrs = append(attrs, "метод", r.Method, "путь", r.URL.Path, "клиент", r.RemoteAddr)
	}
	authLog().Error("внутренняя ошибка аутентификации", attrs...)
}

// internalError — отказ страничного пути входа (браузер получает HTML-страницу
// или простой текст).
func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	logInternalError(r, op, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// internalErrorJSON — тот же отказ для JSON-путей (/api/login, подтверждение
// второго фактора из конфигуратора). Тело ответа прежнее: его разбирают
// клиенты, и менять контракт заодно с журналированием нельзя.
func (h *Handlers) internalErrorJSON(w http.ResponseWriter, r *http.Request, op string, err error) {
	logInternalError(r, op, err)
	http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
}

// internalErrorMsg — отказ со своим текстом для пользователя (например, «не
// удалось построить QR-код»). Причина всё равно уходит в журнал.
func internalErrorMsg(w http.ResponseWriter, r *http.Request, op, msg string, err error) {
	logInternalError(r, op, err)
	http.Error(w, msg, http.StatusInternalServerError)
}
