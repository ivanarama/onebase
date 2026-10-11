package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Документ, созданный кодом и открытый для оформления, уже записан: при его
// сохранении защита «нельзя изменить то, что не видно» восстанавливала поля
// под маской из базы. Пустой телефон роль видит пустым — скрывать там нечего,
// — но и введённый ею номер молча отбрасывался. Пустое под маской заполнить
// можно; заполненное по-прежнему не меняется; hide не заполняется никогда.
func TestUI_SaveCard_MaskedEmptyFieldCanBeFilled(t *testing.T) {
	cases := []struct {
		name   string
		meta   func() *metadata.Entity
		policy auth.FieldPolicies
		stored string
		sent   string
		want   string
	}{
		{name: "mask_tail, пусто — заполняется", meta: uiClientEntity,
			policy: auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}}, stored: "", sent: "(903)222-33-44", want: "(903)222-33-44"},
		{name: "pii без политики (mask_all), пусто — заполняется", meta: piiClientEntity,
			stored: "", sent: "(903)222-33-44", want: "(903)222-33-44"},
		{name: "mask_all, заполнено — не меняется", meta: piiClientEntity,
			stored: "(916)111-22-33", sent: "(903)222-33-44", want: "(916)111-22-33"},
		{name: "mask_all, маска в ответ — не меняется", meta: piiClientEntity,
			stored: "(916)111-22-33", sent: "••••••", want: "(916)111-22-33"},
		{name: "mask_all, пусто, в ответ маска — звёздочки не пишутся", meta: piiClientEntity,
			stored: "", sent: "••••••", want: ""},
		{name: "hide, пусто — не заполняется", meta: uiClientEntity,
			policy: auth.FieldPolicies{"Телефон": {Read: "hide"}}, stored: "", sent: "(903)222-33-44", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat := tc.meta()
			s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
			id := uuid.New()
			initial := map[string]any{"Наименование": "Иванов"}
			if tc.stored != "" {
				initial["Телефон"] = tc.stored
			}
			if err := s.store.Upsert(ctx, cat.Name, id, initial, cat); err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			s.Mount(router)
			user := uiMaskUser([]string{"read", "write"}, tc.policy)
			form := url.Values{"Наименование": {"Петров"}, "Телефон": {tc.sent}}
			r := httptest.NewRequest(http.MethodPost, "/ui/catalog/"+url.PathEscape(cat.Name)+"/"+id.String(), strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r = r.WithContext(auth.ContextWithUser(r.Context(), user))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("сохранение карточки: ожидался 303, получено %d: %s", w.Code, w.Body.String())
			}
			row, err := s.store.GetByID(ctx, cat.Name, id, cat)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(toStringOrEmpty(row["Телефон"])); got != tc.want {
				t.Fatalf("Телефон = %q, ожидалось %q", got, tc.want)
			}
			if row["Наименование"] != "Петров" {
				t.Fatalf("видимое поле должно обновиться, получено %v", row["Наименование"])
			}
		})
	}
}

func piiClientEntity() *metadata.Entity {
	cat := uiClientEntity()
	for i := range cat.Fields {
		if cat.Fields[i].Name == "Телефон" {
			cat.Fields[i].PII = true
		}
	}
	return cat
}

func toStringOrEmpty(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// Номер, который пользователь только что набрал в поле под маской, ответ
// события формы возвращает как есть: маска на обратном пути подменяла его
// звёздочками, и следующее событие или запись присылали на сервер их — номер
// терялся, а с заполнением пустого поля в базу легли бы сами звёздочки.
// Значение из базы (клиент прислал маску) и поставленное обработчиком
// по-прежнему уходят маской.
func TestUI_FormEvent_EchoesTypedMaskedValue(t *testing.T) {
	cat := piiClientEntity()
	form := managedObjectForm(fieldEl("ПолеНаименование", "Объект.Наименование"), fieldEl("ПолеТелефон", "Объект.Телефон"),
		maskEventButton("КнПроверить", "КнПроверитьНажатие"), maskEventButton("КнПодменить", "КнПодменитьНажатие"))
	form.EntityName = cat.Name
	form.ProgramAST = mustParse(t, `
Процедура КнПроверитьНажатие()
КонецПроцедуры

Процедура КнПодменитьНажатие()
	Объект.Телефон = "(111)000-00-00";
КонецПроцедуры
`)
	cat.Forms = []*metadata.FormModule{form}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	router := chi.NewRouter()
	s.Mount(router)
	user := uiMaskUser([]string{"read", "write"}, nil)

	event := func(id uuid.UUID, element, phone string) map[string]any {
		t.Helper()
		body := url.Values{"_id": {id.String()}, "_element": {element}, "_event": {"Нажатие"},
			"Наименование": {"Иванов"}, "Телефон": {phone}}
		r := httptest.NewRequest(http.MethodPost, "/ui/catalog/"+url.PathEscape(cat.Name)+"/form-event", strings.NewReader(body.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		r = r.WithContext(auth.ContextWithUser(r.Context(), user))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		var resp struct {
			Values map[string]any `json:"values"`
			Error  string         `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Error != "" {
			t.Fatalf("form-event: %d %s (%v)", w.Code, w.Body.String(), err)
		}
		return resp.Values
	}
	seed := func(phone string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		fields := map[string]any{"Наименование": "Иванов"}
		if phone != "" {
			fields["Телефон"] = phone
		}
		if err := s.store.Upsert(ctx, cat.Name, id, fields, cat); err != nil {
			t.Fatal(err)
		}
		return id
	}

	empty := seed("")
	if got := event(empty, "КнПроверить", "(903)222-33-44")["Телефон"]; got != "(903)222-33-44" {
		t.Fatalf("набранный номер должен вернуться как есть, получено %v", got)
	}
	if got := event(empty, "КнПодменить", "(903)222-33-44")["Телефон"]; got == "(111)000-00-00" {
		t.Fatalf("значение, поставленное обработчиком, должно уйти маской, получено %v", got)
	}
	filled := seed("(916)111-22-33")
	if got := event(filled, "КнПроверить", "••••••")["Телефон"]; got == "(916)111-22-33" {
		t.Fatalf("значение из базы должно уйти маской, получено %v", got)
	}
}

// hide omits the key even when the client submits a value; only mask_*
// policies may echo that client's unchanged input through the public event API.
func TestUI_FormEvent_SubmittedFieldReadPolicy(t *testing.T) {
	const sent = "(903)222-33-44"
	cases := []struct {
		name     string
		strategy string
		stored   string
	}{
		{name: "hide/arbitrary_submission", strategy: "hide", stored: "(916)111-22-33"},
		{name: "hide/matching_submission", strategy: "hide", stored: sent},
		{name: "hide/empty_stored_field", strategy: "hide", stored: ""},
		{name: "mask_all", strategy: "mask_all"},
		{name: "mask_tail", strategy: "mask_tail"},
		{name: "mask_city", strategy: "mask_city"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat := uiClientEntity()
			form := managedObjectForm(fieldEl("ПолеНаименование", "Объект.Наименование"),
				fieldEl("ПолеТелефон", "Объект.Телефон"), maskEventButton("КнПроверить", "КнПроверитьНажатие"))
			form.EntityName = cat.Name
			form.ProgramAST = mustParse(t, `
Процедура КнПроверитьНажатие()
КонецПроцедуры
`)
			cat.Forms = []*metadata.FormModule{form}
			s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
			id := uuid.New()
			if err := s.store.Upsert(ctx, cat.Name, id,
				map[string]any{"Наименование": "Иванов", "Телефон": tc.stored}, cat); err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			s.Mount(router)
			body := url.Values{"_id": {id.String()}, "_element": {"КнПроверить"}, "_event": {"Нажатие"},
				"Наименование": {"Иванов"}, "Телефон": {sent}}
			r := httptest.NewRequest(http.MethodPost, "/ui/catalog/"+url.PathEscape(cat.Name)+"/form-event", strings.NewReader(body.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
			user := uiMaskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: tc.strategy, Keep: 4}})
			r = r.WithContext(auth.ContextWithUser(r.Context(), user))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			var resp struct {
				OK     bool           `json:"ok"`
				Values map[string]any `json:"values"`
				Error  string         `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || w.Code != http.StatusOK || !resp.OK || resp.Error != "" {
				t.Fatalf("form-event: %d %s (%v)", w.Code, w.Body.String(), err)
			}
			if got := resp.Values["Наименование"]; got != "Иванов" {
				t.Fatalf("unrestricted field = %v, want Иванов", got)
			}
			if tc.strategy == "hide" {
				for key := range resp.Values {
					if strings.EqualFold(key, "Телефон") {
						t.Fatalf("hide must omit the key, got values=%v", resp.Values)
					}
				}
			} else if got := resp.Values["Телефон"]; got != sent {
				t.Fatalf("%s must echo the submitted value, got %v", tc.strategy, got)
			}
		})
	}
}

// Поле под маской в управляемой форме помечено data-ob-protected и показывается
// точками, как пароль; у пользователя без маски пометки нет.
func TestUI_ManagedForm_ProtectedInputRenderedAsPassword(t *testing.T) {
	cat := piiClientEntity()
	form := managedObjectForm(fieldEl("ПолеНаименование", "Объект.Наименование"), fieldEl("ПолеТелефон", "Объект.Телефон"))
	form.EntityName = cat.Name
	cat.Forms = []*metadata.FormModule{form}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	id := uuid.New()
	if err := s.store.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": "Иванов", "Телефон": "(916)111-22-33"}, cat); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	s.Mount(router)
	page := func(user *auth.User) string {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/ui/catalog/"+url.PathEscape(cat.Name)+"/"+id.String(), nil)
		r = r.WithContext(auth.ContextWithUser(r.Context(), user))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET формы: %d %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	masked := page(uiMaskUser([]string{"read", "write"}, nil))
	if !strings.Contains(masked, `name="Телефон" value="••••••" data-ob-protected`) {
		t.Fatalf("поле телефона под маской должно быть помечено data-ob-protected")
	}
	if strings.Contains(masked, "(916)111-22-33") {
		t.Fatal("настоящего номера не должно быть в разметке")
	}
	if strings.Contains(masked, `name="Наименование" value="Иванов" data-ob-protected`) {
		t.Fatal("обычное поле не должно помечаться")
	}
	tail := page(uiMaskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 5}}))
	if !strings.Contains(tail, `name="Телефон" value="•••••••••22-33"`) || strings.Contains(tail, `value="•••••••••22-33" data-ob-protected`) {
		t.Fatal("mask_tail показывает часть номера — поле не точками")
	}
	full := page(uiMaskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: "full"}}))
	if !strings.Contains(full, `name="Телефон" value="(916)111-22-33"`) || strings.Contains(full, `value="(916)111-22-33" data-ob-protected`) {
		t.Fatal("у пользователя без маски значение видно и поле не помечается")
	}
}

func maskEventButton(name, handler string) *metadata.FormElement {
	return &metadata.FormElement{
		Kind: metadata.FormElementButton, Name: name,
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: handler},
	}
}

// Ответ события не стирает набранное за время запроса (managed.js applyValues).
func TestManagedApplyValuesKeepsTypingInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed applyValues typing regression test")
	}
	cmd := exec.Command(node, "--test", "static/managed_apply_typing_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node managed applyValues typing test: %v\n%s", err, output)
	}
}

// The client regression consumes the real saved form-event envelope, then types
// while that response is pending and closes through the production controller.
func TestUI_FormEvent_SaveKeepsLateTypingDirtyInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the saved form-event and close regression")
	}
	cat := piiClientEntity()
	form := managedObjectForm(fieldEl("ПолеТелефон", "Объект.Телефон"),
		maskEventButton("КнЗаписать", "КнЗаписатьНажатие"))
	form.EntityName = cat.Name
	form.ProgramAST = mustParse(t, `
Процедура КнЗаписатьНажатие()
	Объект.Записать();
КонецПроцедуры
`)
	cat.Forms = []*metadata.FormModule{form}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	id := uuid.New()
	if err := s.store.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": "Иванов"}, cat); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	s.Mount(router)
	const phone = "(937)637-32-7"
	body := url.Values{"_id": {id.String()}, "_element": {"КнЗаписать"}, "_event": {"Нажатие"},
		"Наименование": {"Иванов"}, "Телефон": {phone}}
	r := httptest.NewRequest(http.MethodPost, "/ui/catalog/"+url.PathEscape(cat.Name)+"/form-event", strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	r = r.WithContext(auth.ContextWithUser(r.Context(), uiMaskUser([]string{"read", "write"}, nil)))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	var result struct {
		OK      bool           `json:"ok"`
		Dirty   *bool          `json:"dirty"`
		Version int            `json:"version"`
		Values  map[string]any `json:"values"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || !result.OK ||
		result.Dirty == nil || *result.Dirty || result.Version == 0 || result.Values["Телефон"] != phone {
		t.Fatalf("saved form-event envelope: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	stored, err := s.store.GetByID(ctx, cat.Name, id, cat)
	if err != nil || stored["Телефон"] != phone {
		t.Fatalf("handler did not save sent phone: %v (%v)", stored, err)
	}
	fixture := filepath.Join(t.TempDir(), "saved-event.json")
	if err := os.WriteFile(fixture, w.Body.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--test", "--test-name-pattern", "^ordinary saved form event", "static/managed_close_intent_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = append(os.Environ(), "ONEBASE_SAVED_EVENT_RESPONSE="+fixture)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("saved form-event and close regression: %v\n%s", err, output)
	}
}
