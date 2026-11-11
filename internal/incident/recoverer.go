package incident

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"

	oblog "github.com/ivantit66/onebase/internal/logging"
)

// UserFunc достаёт логин из запроса. Параметром, а не прямым обращением к
// internal/auth: incident — листовой пакет, и втягивать в него аутентификацию
// значило бы связать перехват паник с моделью пользователей.
type UserFunc func(*http.Request) string

// Recoverer заменяет chi middleware.Recoverer: кроме журнала и ответа 500 он
// регистрирует инцидент и печатает его код в теле ответа.
//
// Без кода паника выглядит для пользователя как «Internal Server Error» без
// продолжения: воспроизвести её разработчик не может, а связать жалобу с
// конкретной строкой в журнале нечем.
func Recoverer(s *Store, userOf UserFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// ErrAbortHandler — штатный способ оборвать ответ (его кидает,
				// например, обратный прокси при разрыве соединения). Это не
				// инцидент, и http.Server ждёт эту панику обратно.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				text := fmt.Sprintf("%v", rec)
				if errors.Is(r.Context().Err(), context.Canceled) {
					// Клиент уже ушёл (закрыли вкладку, удалили iframe формы,
					// новый запрос отменил прежний), и обработчик оборвался на
					// первой же операции с отменённым контекстом — часто паникой
					// DSL-уровня («Предзагрузка реквизитов …: context canceled»).
					// Отвечать некому, и это не сбой сервера: как serverError в ui,
					// отдаём 499 без инцидента и без ERROR, иначе журнал и монитор
					// получают «паники», которых не было. Контекст запроса
					// отменяется только разрывом соединения; таймауты операций
					// дают DeadlineExceeded и остаются инцидентом.
					oblog.Component("http").Info("запрос прерван клиентом",
						"where", WhereOf(r), "reason", text)
					w.WriteHeader(StatusClientClosedRequest)
					return
				}
				stack := string(debug.Stack())
				var id string
				if s != nil {
					user := ""
					if userOf != nil {
						user = userOf(r)
					}
					id = s.Record(Record{
						Kind:  KindPanic,
						Where: WhereOf(r),
						Text:  text,
						Stack: stack,
						User:  user,
					}).ID
				}
				oblog.Component("http").Error("паника в обработчике",
					"incident", id, "where", WhereOf(r), "panic", text, "stack", stack)
				http.Error(w, Message(id), http.StatusInternalServerError)
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// StatusClientClosedRequest — 499 nginx: клиент закрыл соединение до ответа.
const StatusClientClosedRequest = 499

// Message — то, что видит пользователь вместо страницы. Код инцидента идёт
// последним и отдельным предложением, чтобы его было видно и в узком тосте, и
// в скриншоте из мессенджера.
func Message(id string) string {
	if id == "" {
		return "Внутренняя ошибка сервера"
	}
	return "Внутренняя ошибка сервера. Код инцидента: " + id
}

// WithCode дописывает код инцидента к готовому тексту ошибки. Используется там,
// где обработчик уже сформулировал причину и локализовал её.
func WithCode(text, id string) string {
	if id == "" {
		return text
	}
	return text + " · код инцидента: " + id
}
