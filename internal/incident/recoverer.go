package incident

import (
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
				stack := string(debug.Stack())
				text := fmt.Sprintf("%v", rec)
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
