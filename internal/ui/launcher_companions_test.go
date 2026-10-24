package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	orun "github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// GET /ui/launcher/companions — ключи сопутствующих программ пользователя для
// окна Предприятия лаунчера. Ключи называет функция общего модуля
// СопутствующиеПрограммыПользователя (Массив или строка через запятую);
// платформа приводит их к нижнему регистру, убирает повторы и всё, что не
// похоже на ключ: путь к программе сервер передать не может. Нет функции,
// ошибка в ней, запрос без пользователя — пустой список. Путь: маршрут
// зарегистрирован Mount.
func TestLauncherCompanionsEndpoint(t *testing.T) {
	cases := []struct {
		name, module string
		user         bool
		want         string
	}{
		{"массив ключей", "Функция СопутствующиеПрограммыПользователя() Экспорт\n  Ключи = Новый Массив;\n  Ключи.Добавить(\"Callista\");\n  Ключи.Добавить(\"callista\");\n  Ключи.Добавить(\"C:\\Windows\\calc.exe\");\n  Возврат Ключи;\nКонецФункции\n", true, "callista"},
		{"строка через запятую", "Функция СопутствующиеПрограммыПользователя() Экспорт\n  Возврат \"callista, notes\";\nКонецФункции\n", true, "callista,notes"},
		{"ключ по пользователю", "Функция СопутствующиеПрограммыПользователя() Экспорт\n  Если ИмяПользователя() = \"oper\" Тогда\n    Возврат \"callista\";\n  КонецЕсли;\n  Возврат \"\";\nКонецФункции\n", true, "callista"},
		{"нет функции", "Процедура Другое() Экспорт\nКонецПроцедуры\n", true, ""},
		{"ошибка в функции", "Функция СопутствующиеПрограммыПользователя() Экспорт\n  ВызватьИсключение \"нет данных\";\nКонецФункции\n", true, ""},
		{"без пользователя", "Функция СопутствующиеПрограммыПользователя() Экспорт\n  Возврат \"callista\";\nКонецФункции\n", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := storage.ConnectSQLite(t.Context(), filepath.Join(t.TempDir(), "companions.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.Migrate(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			reg := orun.NewRegistry()
			reg.LoadModules(map[string]*ast.Program{"Телефония": mustParse(t, tc.module)})
			interp := interpreter.New()
			interp.LookupProc = reg.GetModuleProc
			srv := New(reg, db, interp, auth.NewRepo(db), Config{}, nil)
			defer srv.BeginShutdown()
			router := chi.NewRouter()
			router.Group(func(r chi.Router) {
				if tc.user {
					r.Use(func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{ID: "u-oper", Login: "oper"})))
						})
					})
				}
				srv.Mount(r)
			})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/launcher/companions", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var resp struct {
				Keys []string `json:"keys"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Keys == nil {
				t.Fatalf("ответ %s: %v", rec.Body.String(), err)
			}
			if got := strings.Join(resp.Keys, ","); got != tc.want {
				t.Fatalf("ключи %q, ожидалось %q", got, tc.want)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Error("ответ про пользователя не должен кешироваться")
			}
		})
	}
}
