package ui

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// План 37, этап 8: рантайм событий managed-форм. Проверяем happy-path:
// форма с кнопкой «Тест» и обработчиком «Нажатие», который дёргает
// Сообщить("hello"). POST /form-event возвращает JSON с messages: ["hello"].
func TestHandleManagedFormEvent_ButtonFiresSoobshchit(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ТестНажатие()
	Сообщить("hello");
КонецПроцедуры
`, map[metadata.FormEventType]string{}, // form-level пусто
		// element-level: кнопка «КнопкаТест» → «ТестНажатие»
		[]*metadata.FormElement{
			{
				Kind: metadata.FormElementButton,
				Name: "КнопкаТест",
				Handlers: map[metadata.FormEventType]string{
					metadata.FormEventOnClick: "ТестНажатие",
				},
			},
		})

	body := url.Values{}
	body.Set("_element", "КнопкаТест")
	body.Set("_event", string(metadata.FormEventOnClick))
	body.Set("_kind", "object")
	body.Set("Наименование", "X")

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())

	if !resp.OK {
		t.Fatalf("ожидался ok=true, получили ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || resp.Messages[0] != "hello" {
		t.Errorf("messages=%v, ожидалось [\"hello\"]", resp.Messages)
	}
}

// #608: событие управляемой формы, обработчик которого сам объект НЕ записал,
// не должно обновлять _version клиента. Иначе версия, поднятая параллельной
// чужой записью между отрисовкой и событием, молча приезжает клиенту, и
// последующая «Записать» проходит проверку версии, затирая чужие правки.
func TestHandleManagedFormEvent_VersionNotForwardedWithoutWrite(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ТестНажатие()
	Сообщить("hi");
КонецПроцедуры
`, nil, []*metadata.FormElement{
		{
			Kind: metadata.FormElementButton,
			Name: "КнопкаТест",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnClick: "ТестНажатие",
			},
		},
	})

	ctx := context.Background()
	id := uuid.New()
	// Пользователь A отрисовал форму, держит прочитанную версию.
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "A"}, ent); err != nil {
		t.Fatal(err)
	}
	// Пользователь B параллельно сохранил ту же запись → версия в БД уехала вперёд.
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "B"}, ent); err != nil {
		t.Fatal(err)
	}

	body := url.Values{}
	body.Set("_element", "КнопкаТест")
	body.Set("_event", string(metadata.FormEventOnClick))
	body.Set("_kind", "object")
	body.Set("_id", id.String())
	body.Set("Наименование", "A")

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if resp.Version != 0 {
		t.Errorf("version=%d, ожидался 0 — обработчик объект не писал, версию клиенту слать нельзя (иначе оптимистическая блокировка ослепла)", resp.Version)
	}
}

// Если у элемента нет привязанной процедуры — handler отдаёт ok=true
// без messages и без error. Это «декларативное» событие, не ошибка.
func TestHandleManagedFormEvent_NoHandlerIsOK(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, ``, nil,
		// элемент есть, но без Handlers
		[]*metadata.FormElement{
			{Kind: metadata.FormElementButton, Name: "КнопкаПусто"},
		})

	body := url.Values{}
	body.Set("_element", "КнопкаПусто")
	body.Set("_event", string(metadata.FormEventOnClick))

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())

	if !resp.OK {
		t.Errorf("без привязки обработчика ждали ok=true, получили ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 0 {
		t.Errorf("ждали 0 сообщений, получили %v", resp.Messages)
	}
}

// Привязка есть, но в .form.os нет процедуры с таким именем — handler
// возвращает ok=true и предупреждающее сообщение, а не 500.
func TestHandleManagedFormEvent_HandlerNotFoundInAST(t *testing.T) {
	// AST модуля непустой (есть какая-то процедура), но именно
	// «НесуществующаяПроцедура» в нём не объявлена — handler должен
	// вернуть OK=true с предупреждением.
	srv, ent := setupManagedEventsServer(t, `
Процедура НеТаЧтоНужно()
КонецПроцедуры
`, nil,
		[]*metadata.FormElement{
			{
				Kind: metadata.FormElementButton,
				Name: "КнопкаНет",
				Handlers: map[metadata.FormEventType]string{
					metadata.FormEventOnClick: "НесуществующаяПроцедура",
				},
			},
		})

	body := url.Values{}
	body.Set("_element", "КнопкаНет")
	body.Set("_event", string(metadata.FormEventOnClick))

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())

	if !resp.OK {
		t.Errorf("ждали ok=true c предупреждением, получили error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || !strings.Contains(resp.Messages[0], "НесуществующаяПроцедура") {
		t.Errorf("ждали предупреждение про НесуществующуюПроцедуру, получили %v", resp.Messages)
	}
}

// Ошибка времени выполнения DSL отдаётся в JSON как ok=false + error,
// а не как HTTP 500. Клиент покажет красный баннер, форма не закроется.
func TestHandleManagedFormEvent_DSLRuntimeError(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура Бум()
	ВызватьИсключение("намеренный сбой");
КонецПроцедуры
`, nil,
		[]*metadata.FormElement{
			{
				Kind: metadata.FormElementButton,
				Name: "КнопкаБум",
				Handlers: map[metadata.FormEventType]string{
					metadata.FormEventOnClick: "Бум",
				},
			},
		})

	body := url.Values{}
	body.Set("_element", "КнопкаБум")
	body.Set("_event", string(metadata.FormEventOnClick))

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())

	if resp.OK {
		t.Errorf("ждали ok=false при делении на 0, получили ok=true")
	}
	if resp.Error == "" {
		t.Errorf("ждали непустой error при DSL-ошибке")
	}
}

// ── вспомогательные функции ──────────────────────────────────────────

// setupManagedEventsServer собирает минимальный *Server с одним справочником
// «Контрагент», к которому подключена managed-форма с указанным .form.os и
// деревом элементов. Возвращает сервер и entity для удобства теста.
//
// formOSSource — текст модуля .form.os (может быть пустым); formHandlers —
// form-level обработчики; rootElements — корень дерева формы.
func setupManagedEventsServer(t *testing.T, formOSSource string, formHandlers map[metadata.FormEventType]string, rootElements []*metadata.FormElement) (*Server, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ent := &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}

	// ProgramAST хранится как any → typed-nil (*ast.Program)(nil) ≠ nil
	// при проверке `progAny == nil` в handler'е. Поэтому записываем только
	// реальный, не-nil AST; иначе оставляем поле как untyped nil.
	var astField any
	if strings.TrimSpace(formOSSource) != "" {
		astField = mustParse(t, formOSSource)
	}

	form := &metadata.FormModule{
		Name:       "ФормаОбъекта",
		Kind:       "object",
		EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Title:      map[string]string{"ru": "Контрагент"},
		Elements:   rootElements,
		Handlers:   formHandlers,
		ProgramAST: astField,
	}
	ent.Forms = []*metadata.FormModule{form}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	s := &Server{
		store:    db,
		reg:      registry,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}
	return s, ent
}

// executeFormEvent имитирует POST /ui/{kind}/{entity}/form-event через httptest
// и chi.RouteContext (минуя реальный router, чтобы тест не зависел от Mount).
func executeFormEvent(t *testing.T, s *Server, ent *metadata.Entity, body url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/ui/catalog/"+ent.Name+"/form-event",
		strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "catalog")
	rctx.URLParams.Add("entity", ent.Name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	s.handleManagedFormEvent(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}
	return rec
}

func decodeFormEventResponse(t *testing.T, b []byte) formEventResponse {
	t.Helper()
	var resp formEventResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("json: %v; body=%s", err, string(b))
	}
	return resp
}

// #621: обработчик события управляемой формы оставил открытой DSL-транзакцию и
// вышел с ошибкой. Перечитывание после Run обязано идти живым контекстом
// (внутри той же транзакции), а не r.Context(): на SQLite пул — одно соединение,
// и запрос по r.Context() ждал бы второе, занятое транзакцией, — событие вешало
// бы всю базу ровно на пути возврата ошибки. Событие обязано ВЕРНУТЬ ошибку.
func TestHandleManagedFormEvent_OpenTxDoesNotHang(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура Бум()
	НачатьТранзакцию();
	ВызватьИсключение("боль");
КонецПроцедуры
`, nil, []*metadata.FormElement{
		{
			Kind: metadata.FormElementButton,
			Name: "КнопкаБум",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnClick: "Бум",
			},
		},
	})

	// Существующая запись: гейт _id включает перечитывание после Run.
	ctx := context.Background()
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "A"}, ent); err != nil {
		t.Fatal(err)
	}

	body := url.Values{}
	body.Set("_element", "КнопкаБум")
	body.Set("_event", string(metadata.FormEventOnClick))
	body.Set("_kind", "object")
	body.Set("_id", id.String())
	body.Set("Наименование", "A")

	// Гоняем в горутине: до фикса перечитывание виснет на единственном соединении
	// SQLite, и без таймаута тест не падал бы, а зависал бы вместе с прогоном.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest("POST", "/ui/catalog/"+ent.Name+"/form-event",
			strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("kind", "catalog")
		rctx.URLParams.Add("entity", ent.Name)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		srv.handleManagedFormEvent(rec, req)
		done <- rec
	}()

	select {
	case rec := <-done:
		if rec.Code != 200 {
			t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
		}
		resp := decodeFormEventResponse(t, rec.Body.Bytes())
		if resp.OK {
			t.Fatalf("ожидалась ошибка обработчика, получили ok=true")
		}
		if resp.Error == "" {
			t.Fatalf("ожидался непустой error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("событие зависло: перечитывание после Run ушло мимо открытой транзакции (#621)")
	}
}
