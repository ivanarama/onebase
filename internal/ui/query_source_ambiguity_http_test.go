package ui

import (
	"context"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Exercises the ordinary processor form-event route, with no report context
// and no explicit field policies. A nil resolver must not erase default PII.
func TestNonReportQuerySourceAmbiguityHTTP(t *testing.T) {
	for _, mode := range []string{"ambiguous", "unambiguous_pii", "unambiguous_public"} {
		t.Run(mode, func(t *testing.T) {
			form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Read", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Read"}})
			form.ProgramAST = mustParse(t, `Процедура Read()
          З=Новый Запрос;З.Текст="ВЫБРАТЬ Phone ИЗ РегистрНакопления.Shared";
          Для Каждого Р Из З.Выполнить() Цикл Сообщить(Р.Phone);КонецЦикла;
        КонецПроцедуры`)
			proc := &processor.Processor{Name: "OrdinaryQuery", Forms: []*metadata.FormModule{form}}
			s, db := newProcessorFormEventExecutionServer(t, proc, nil)
			t.Cleanup(s.Close)
			reg := &metadata.Register{Name: "Shared", Dimensions: []metadata.Field{{Name: "Phone", Type: "string", PII: mode != "unambiguous_public"}}, Resources: []metadata.Field{{Name: "Qty", Type: "number"}}}
			s.reg.Load(runtime.LoadOptions{Registers: []*metadata.Register{reg}})
			if mode == "ambiguous" {
				s.reg.LoadAccountRegisters([]*metadata.AccountRegister{{Name: "Shared", Resources: []metadata.Field{{Name: "Qty", Type: "number"}}}}, nil)
			}
			ctx := context.Background()
			if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
				t.Fatal(err)
			}
			const raw = "synthetic_private_probe"
			if err := db.WriteMovements(ctx, reg.Name, "Probe", uuid.New(), []map[string]any{{"Phone": raw, "Qty": 1}}, reg, nil); err != nil {
				t.Fatal(err)
			}
			user := &auth.User{Login: "ordinary_reader", Roles: []*auth.Role{{Name: "Reader", Permissions: auth.Permission{Processors: map[string][]string{proc.Name: {"run"}}, Registers: map[string][]string{reg.Name: {"read"}}}}}}
			router := chi.NewRouter()
			router.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), user)))
				})
			})
			s.Mount(router)
			values := url.Values{"_element": {"Read"}, "_event": {string(metadata.FormEventOnClick)}}
			req := httptest.NewRequest(http.MethodPost, "/ui/processor/OrdinaryQuery/form-event", strings.NewReader(values.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("public form event HTTP%d: %s", rec.Code, rec.Body.String())
			}
			response := decodeFormEventResponse(t, rec.Body.Bytes())
			if mode == "ambiguous" {
				if response.OK || strings.Contains(rec.Body.String(), raw) {
					t.Fatalf("unresolved source released default PII: %s", rec.Body.String())
				}
				if !strings.Contains(response.Error, "защищ") && !strings.Contains(response.Error, "источник") {
					t.Fatalf("wrong rejection stage: %+v", response)
				}
			} else {
				if !response.OK || len(response.Messages) != 1 {
					t.Fatalf("ordinary source changed: %s", rec.Body.String())
				}
				if mode == "unambiguous_pii" && (response.Messages[0] == raw || !strings.Contains(response.Messages[0], "•")) {
					t.Fatalf("default PII mask lost: %v", response.Messages)
				}
				if mode == "unambiguous_public" && response.Messages[0] != raw {
					t.Fatalf("public field changed: %v", response.Messages)
				}
			}
		})
	}
}
