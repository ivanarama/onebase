package ui

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func setupCheckboxFixture(t *testing.T) (*Server, *metadata.Entity, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ent := &metadata.Entity{
		Name: "Задача", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Активна", Type: metadata.FieldTypeBool},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, ent.Name, id,
		map[string]any{"Наименование": "З-1", "Активна": true}, ent); err != nil {
		t.Fatal(err)
	}

	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
			{Kind: metadata.FormElementType("Флажок"), Name: "Активна", DataPath: "Объект.Активна"},
			{
				Kind: metadata.FormElementButton, Name: "КнопкаТест",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Тест"},
			},
		},
		ProgramAST: mustParse(t, `
Процедура Тест()
	Сообщить("ok");
КонецПроцедуры
`),
	}
	ent.Forms = []*metadata.FormModule{form}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	srv := &Server{store: db, reg: reg, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	srv.entitySvc = srv.newEntityService(nil)
	return srv, ent, id
}

// Пользователь снял галку → браузер не шлёт ключ «Активна». После события формы
// значение в ответе обязано быть ЛОЖЬЮ — и обязано в ответе БЫТЬ.
//
// Прежняя версия проверяла только «если ключ есть, он не true», то есть
// исчезновение поля из ответа считала успехом (#888). А исчезновение — это
// отдельный отказ, и он хуже: клиент применяет values к форме, и ключа, которого
// в ответе нет, он не трогает. Галка осталась бы взведённой на экране, хотя
// пользователь её снял, — ровно то поведение, ради недопущения которого тест и
// писался.
func TestVerify_СнятаяГалкаНеВозвращается(t *testing.T) {
	srv, ent, id := setupCheckboxFixture(t)

	body := url.Values{}
	body.Set("_id", id.String())
	body.Set("Наименование", "З-1")
	body.Set("_element", "КнопкаТест")
	body.Set("_event", string(metadata.FormEventOnClick))
	body.Set("_kind", "object")

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())

	value, ok := lookupFormValue(resp.Values, "Активна")
	if !ok {
		t.Fatalf("поле «Активна» исчезло из ответа события: %#v\n"+
			"клиент не трогает ключи, которых в ответе нет, — снятая галка осталась бы "+
			"взведённой на экране", resp.Values)
	}
	if isTruthyFormValue(value) {
		t.Fatalf("снятая галка вернулась взведённой: %#v (%T)", value, value)
	}
}

// Обратная сторона того же контракта: взведённая галка возвращается взведённой.
// Без этой половины «поле всегда ложь» прошло бы обе проверки.
func TestVerify_ВзведённаяГалкаВозвращаетсяВзведённой(t *testing.T) {
	srv, ent, id := setupCheckboxFixture(t)

	body := url.Values{}
	body.Set("_id", id.String())
	body.Set("Наименование", "З-1")
	body.Set("Активна", "true")
	body.Set("_element", "КнопкаТест")
	body.Set("_event", string(metadata.FormEventOnClick))
	body.Set("_kind", "object")

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())

	value, ok := lookupFormValue(resp.Values, "Активна")
	if !ok {
		t.Fatalf("поле «Активна» исчезло из ответа события: %#v", resp.Values)
	}
	if !isTruthyFormValue(value) {
		t.Fatalf("взведённая галка вернулась снятой: %#v (%T)", value, value)
	}
}

// lookupFormValue ищет ключ без учёта регистра: Object.Set хранит имена в
// нижнем регистре, а нормализация возвращает исходный — тест не должен зависеть
// от того, какая из двух форм доехала.
func lookupFormValue(values map[string]any, name string) (any, bool) {
	if v, ok := values[name]; ok {
		return v, true
	}
	for k, v := range values {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return nil, false
}

// isTruthyFormValue трактует значение так же, как клиент: непустая строка кроме
// «false»/«0», true, ненулевое число.
func isTruthyFormValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "", "false", "0":
			return false
		}
		return true
	case float64:
		return t != 0
	case int:
		return t != 0
	}
	return false
}
