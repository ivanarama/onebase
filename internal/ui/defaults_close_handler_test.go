package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Новый объект управляемой формы записывается не только кнопкой «Записать».
// «ОК», «Да» в диалоге закрытия и «Записать и выбрать» идут через close-intent,
// а Объект.Записать() из обработчика — через свою обёртку объекта. До правки
// умолчания и ПриСозданииНового (#1189) доезжали до базы только с «Записать»:
// та же форма писала неразмещённые реквизиты пустыми в зависимости от нажатой
// кнопки. Тесты идут через публичные входы — смонтированный close-intent и
// обработчик form-event, как браузер.

// newDefaultsFormServer — документ «Реализация» (defaultsEntities) с
// управляемой формой, на которой размещён только Номер.
func newDefaultsFormServer(t *testing.T, objectModule, formModule string, formElements ...*metadata.FormElement) (*Server, context.Context, []*metadata.Entity) {
	t.Helper()
	ents := defaultsEntities()
	form := managedObjectForm(append([]*metadata.FormElement{fieldEl("ПолеНомер", "Объект.Номер")}, formElements...)...)
	form.EntityName = ents[1].Name
	if strings.TrimSpace(formModule) != "" {
		form.ProgramAST = mustParse(t, formModule)
	}
	ents[1].Forms = []*metadata.FormModule{form}
	programs := map[string]string{}
	if strings.TrimSpace(objectModule) != "" {
		programs[ents[1].Name] = objectModule
	}
	s, ctx := newSubmitTestServerWithPrograms(t, ents, programs)
	return s, ctx, ents
}

func newDocCloseIntentBody(mode, reason string) url.Values {
	return url.Values{
		"_close_intent_id": {uuid.NewString()},
		"_close_epoch":     {formCloseProcessEpoch},
		"_close_issued_at": {strconv.FormatInt(time.Now().UnixMilli(), 10)},
		"_close_reason":    {reason},
		"_close_mode":      {mode},
		"_close_client":    {uuid.NewString()},
		"_kind":            {"object"},
		"Номер":            {"0001"},
	}
}

func onlyDefaultsDoc(t *testing.T, s *Server, ctx context.Context, doc *metadata.Entity) map[string]any {
	t.Helper()
	rows, err := s.store.List(ctx, doc.Name, doc, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ожидалась одна запись, получено %d", len(rows))
	}
	return rows[0]
}

func TestDefaults_ОКИДаВДиалогеЗакрытияЗаполняютНеразмещённые(t *testing.T) {
	for _, tc := range []struct {
		name, mode, reason string
		popup              bool
	}{
		{name: "ОК", mode: "save", reason: "ok"},
		{name: "Да в диалоге закрытия", mode: "save", reason: "close"},
		{name: "Провести и закрыть", mode: "post", reason: "post_and_close"},
		{name: "Записать и выбрать", mode: "save_and_select", reason: "ok", popup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx, ents := newDefaultsFormServer(t, "", "")
			if tc.mode == "post" {
				ents[1].Posting = true
			}
			orgID := seedSingleOrg(t, s, ctx, ents[0])
			body := newDocCloseIntentBody(tc.mode, tc.reason)
			if tc.popup {
				body.Set("_popup", "1")
			}
			rec := executeFormCloseIntent(t, s, ents[1], body)
			resp := decodeCloseIntentResponse(t, rec)
			if resp.Close == nil || !resp.Close.Saved {
				t.Fatalf("close-intent не записал объект: %d %s", rec.Code, rec.Body.String())
			}
			row := onlyDefaultsDoc(t, s, ctx, ents[1])
			if got := refString(row["Организация"]); got != orgID.String() {
				t.Errorf("Организация = %v, ожидался %s — default «единственный» не доехал", row["Организация"], orgID)
			}
			if got := row["Комментарий"]; got != "по умолчанию" {
				t.Errorf("Комментарий = %v, ожидался литеральный дефолт", got)
			}
		})
	}
}

func TestDefaults_ОКЗоветПриСозданииНового(t *testing.T) {
	s, ctx, ents := newDefaultsFormServer(t, `Процедура ПриСозданииНового(Объект)
  Объект.Комментарий = "из хука";
КонецПроцедуры`, "")
	seedSingleOrg(t, s, ctx, ents[0])
	rec := executeFormCloseIntent(t, s, ents[1], newDocCloseIntentBody("save", "ok"))
	if resp := decodeCloseIntentResponse(t, rec); resp.Close == nil || !resp.Close.Saved {
		t.Fatalf("«ОК» не записал объект: %d %s", rec.Code, rec.Body.String())
	}
	if got := onlyDefaultsDoc(t, s, ctx, ents[1])["Комментарий"]; got != "из хука" {
		t.Errorf("Комментарий = %v, ожидалось значение из ПриСозданииНового", got)
	}
}

// Ошибка ПриСозданииНового на записи останавливает «ОК» так же, как «Записать»:
// частично инициализированный объект в базу не попадает, форма остаётся
// открытой с текстом ошибки.
func TestDefaults_ОКНеПишетПослеОшибкиПриСозданииНового(t *testing.T) {
	s, ctx, ents := newDefaultsFormServer(t, `Процедура ПриСозданииНового(Объект)
  ВызватьИсключение("инициализация не выполнена");
КонецПроцедуры`, "")
	rec := executeFormCloseIntent(t, s, ents[1], newDocCloseIntentBody("save", "ok"))
	resp := decodeCloseIntentResponse(t, rec)
	if resp.Close == nil || resp.Close.Allowed || resp.Close.Saved {
		t.Fatalf("«ОК» после ошибки хука закрыл или записал форму: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(resp.Error, "инициализация не выполнена") {
		t.Fatalf("ответ не называет ошибку ПриСозданииНового: %s", rec.Body.String())
	}
	assertNoManagedDocs(t, s, ctx, ents[1])
}

// executeDefaultsFormEvent — POST form-event обработчика кнопки новой формы.
func executeDefaultsFormEvent(t *testing.T, s *Server, doc *metadata.Entity, element string, extra url.Values) *httptest.ResponseRecorder {
	t.Helper()
	body := url.Values{"_element": {element}, "_event": {"Нажатие"}, "Номер": {"0001"}}
	for k, v := range extra {
		body[k] = v
	}
	req := httptest.NewRequest(http.MethodPost, "/ui/document/"+doc.Name+"/form-event", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "document")
	rctx.URLParams.Add("entity", doc.Name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.handleManagedFormEvent(rec, req)
	return rec
}

func writeButton(name, handler string) *metadata.FormElement {
	return &metadata.FormElement{
		Kind: metadata.FormElementButton, Name: name,
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: handler},
	}
}

func TestDefaults_ЗаписатьИзОбработчикаФормы(t *testing.T) {
	s, ctx, ents := newDefaultsFormServer(t, "", `
Процедура КнЗаписатьНажатие()
	Объект.Записать();
КонецПроцедуры
`, writeButton("КнЗаписать", "КнЗаписатьНажатие"))
	orgID := seedSingleOrg(t, s, ctx, ents[0])

	rec := executeDefaultsFormEvent(t, s, ents[1], "КнЗаписать", nil)
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("обработчик не записал объект: %d %s", rec.Code, rec.Body.String())
	}
	row := onlyDefaultsDoc(t, s, ctx, ents[1])
	if got := refString(row["Организация"]); got != orgID.String() {
		t.Errorf("Организация = %v, ожидался %s", row["Организация"], orgID)
	}
	if got := row["Комментарий"]; got != "по умолчанию" {
		t.Errorf("Комментарий = %v, ожидался литеральный дефолт", got)
	}
}

// Значение, которое обработчик сам присвоил неразмещённому реквизиту, главнее
// умолчания: дефолт — то, с чего начинают, а не то, чем платформа исправляет
// сделанное.
func TestDefaults_ЗаписатьИзОбработчикаНеПеретираетПрисвоенное(t *testing.T) {
	s, ctx, ents := newDefaultsFormServer(t, "", `
Процедура КнЗаписатьНажатие()
	Объект.Комментарий = "задал обработчик";
	Объект.Записать();
КонецПроцедуры
`, writeButton("КнЗаписать", "КнЗаписатьНажатие"))
	seedSingleOrg(t, s, ctx, ents[0])

	rec := executeDefaultsFormEvent(t, s, ents[1], "КнЗаписать", nil)
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("обработчик не записал объект: %d %s", rec.Code, rec.Body.String())
	}
	if got := onlyDefaultsDoc(t, s, ctx, ents[1])["Комментарий"]; got != "задал обработчик" {
		t.Errorf("Комментарий = %v, умолчание перетёрло значение обработчика", got)
	}
}

// Явное Неопределено — тоже присваивание обработчика. По значению оно
// неотличимо от реквизита, которого форма не прислала (оба nil), и раньше
// наложение подставляло вместо очищения литеральный дефолт.
func TestDefaults_ЗаписатьИзОбработчикаНеПеретираетЯвноеНеопределено(t *testing.T) {
	s, ctx, ents := newDefaultsFormServer(t, "", `
Процедура КнЗаписатьНажатие()
	Объект.Комментарий = Неопределено;
	Объект.Записать();
КонецПроцедуры
`, writeButton("КнЗаписать", "КнЗаписатьНажатие"))
	orgID := seedSingleOrg(t, s, ctx, ents[0])

	rec := executeDefaultsFormEvent(t, s, ents[1], "КнЗаписать", nil)
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("обработчик не записал объект: %d %s", rec.Code, rec.Body.String())
	}
	row := onlyDefaultsDoc(t, s, ctx, ents[1])
	if got := row["Комментарий"]; got != nil && got != "" {
		t.Errorf("Комментарий = %v, умолчание перетёрло явное Неопределено обработчика", got)
	}
	// Неприсвоенный неразмещённый реквизит по-прежнему получает умолчание.
	if got := refString(row["Организация"]); got != orgID.String() {
		t.Errorf("Организация = %v, ожидался %s", row["Организация"], orgID)
	}
}

// ПередЗакрытием без записи формы («Нет» в диалоге) тоже может записать новый
// объект сам — через ту же обёртку, что обработчик кнопки.
func TestDefaults_ЗаписатьИзПередЗакрытием(t *testing.T) {
	ents := defaultsEntities()
	form := managedObjectForm(fieldEl("ПолеНомер", "Объект.Номер"))
	form.EntityName = ents[1].Name
	form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
	form.ProgramAST = mustParse(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Объект.Записать();
КонецПроцедуры
`)
	ents[1].Forms = []*metadata.FormModule{form}
	s, ctx := newSubmitTestServer(t, ents)
	orgID := seedSingleOrg(t, s, ctx, ents[0])

	rec := executeFormCloseIntent(t, s, ents[1], newDocCloseIntentBody("discard", "close"))
	if resp := decodeCloseIntentResponse(t, rec); resp.Close == nil || !resp.Close.Saved {
		t.Fatalf("ПередЗакрытием не записал объект: %d %s", rec.Code, rec.Body.String())
	}
	row := onlyDefaultsDoc(t, s, ctx, ents[1])
	if got := refString(row["Организация"]); got != orgID.String() {
		t.Errorf("Организация = %v, ожидался %s", row["Организация"], orgID)
	}
	if got := row["Комментарий"]; got != "по умолчанию" {
		t.Errorf("Комментарий = %v, ожидался литеральный дефолт", got)
	}
}
