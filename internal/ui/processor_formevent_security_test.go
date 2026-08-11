package ui

// #4: form-event обработки обязан применять тот же trust-гейт, что и processorRun.
// Недоверенную внешнюю обработку через /form-event может запускать только админ —
// иначе неадмин обходил бы canRunExternalProc и исполнял произвольный DSL.

import (
	"context"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestProcessorFormEvent_TrustGate(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}

	form := &metadata.FormModule{
		Name:       "ФормаОбработки",
		Kind:       "object",
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind:     metadata.FormElementButton,
			Name:     "Выполнить",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Выполнить"},
		}},
	}
	// Внешняя НЕдоверенная обработка с управляемой формой.
	proc := &processor.Processor{Name: "ВнешняяОбр", External: true, Trusted: false, Forms: []*metadata.FormModule{form}}

	registry := runtime.NewRegistry()
	registry.LoadProcessors([]*processor.Processor{proc})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	// Создаём пользователя → HasUsers()==true, поэтому isAdmin(запрос без
	// пользователя в контексте) == false (как в ai_tools_test).
	if _, err := authRepo.Create(ctx, "clerk", "password", "Клерк", false); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		store:    db,
		reg:      registry,
		interp:   interp,
		authRepo: authRepo,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}

	fire := func() formEventResponse {
		body := url.Values{}
		body.Set("_element", "Выполнить")
		body.Set("_event", string(metadata.FormEventOnClick))
		req := httptest.NewRequest("POST", "/ui/processor/ВнешняяОбр/form-event", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("name", "ВнешняяОбр")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		s.handleProcessorFormEvent(rec, req)
		return decodeFormEventResponse(t, rec.Body.Bytes())
	}

	// Недоверенная + неадмин → отказ.
	if resp := fire(); resp.Error != "доступ запрещён" {
		t.Fatalf("недоверенную внешнюю обработку неадмин не должен запускать; got error=%q ok=%v", resp.Error, resp.OK)
	}

	// Доверенная → trust-гейт пропускает. Явно привязанный, но отсутствующий в
	// .form.os обработчик теперь даёт диагностическую ошибку и не проваливается
	// в глобальный fallback Выполнить.
	proc.Trusted = true
	if resp := fire(); resp.Error == "доступ запрещён" || !strings.Contains(resp.Error, "не найдена в .form.os") {
		t.Fatalf("доверенная внешняя обработка должна пройти trust-гейт и вернуть ошибку привязки; got %q", resp.Error)
	}
}
