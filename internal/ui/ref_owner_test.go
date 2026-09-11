package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// ownerFixture — контрагент с двумя договорами и один договор чужого контрагента.
type ownerFixture struct {
	server      *Server
	owner       *metadata.Entity
	subordinate *metadata.Entity
	holder      *metadata.Entity
	contractor  uuid.UUID
	other       uuid.UUID
}

func newOwnerFixture(t *testing.T) ownerFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "owner.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	contractors := &metadata.Entity{
		Name: "Контрагент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	contracts := &metadata.Entity{
		Name: "Договор", Kind: metadata.KindCatalog, Owner: "Контрагент",
		Fields: []metadata.Field{
			{Name: metadata.StandardOwnerField, ID: metadata.StandardOwnerFieldID, Type: "reference:Контрагент", RefEntity: "Контрагент"},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	order := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Контрагент", Type: "reference:Контрагент", RefEntity: "Контрагент"},
			{Name: "Договор", Type: "reference:Договор", RefEntity: "Договор"},
		},
	}
	entities := []*metadata.Entity{contractors, contracts, order}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}
	our, other := uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{our: "ООО «Наш»", other: "ООО «Чужой»"} {
		if err := db.Upsert(ctx, contractors.Name, id, map[string]any{"наименование": name}, contractors); err != nil {
			t.Fatal(err)
		}
	}
	rows := []struct {
		name  string
		owner uuid.UUID
	}{
		{"Договор 1 нашего", our},
		{"Договор 2 нашего", our},
		{"Договор чужого", other},
	}
	for _, row := range rows {
		if err := db.Upsert(ctx, contracts.Name, uuid.New(), map[string]any{
			"наименование": row.name,
			"владелец":     row.owner.String(),
		}, contracts); err != nil {
			t.Fatal(err)
		}
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	return ownerFixture{
		server: &Server{reg: reg, store: db}, owner: contractors,
		subordinate: contracts, holder: order, contractor: our, other: other,
	}
}

func refOptionLabels(t *testing.T, body []byte) []string {
	t.Helper()
	var resp struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("разбор ответа подбора: %v", err)
	}
	out := make([]string, 0, len(resp.Items))
	for _, it := range resp.Items {
		out = append(out, str(it["_label"]))
	}
	return out
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// Подбор подчинённого справочника показывает только элементы владельца: договор
// чужого контрагента в списке появиться не должен — иначе отбор бесполезен, а
// пользователь выберет чужой договор и не заметит.
func TestRefOptionsFiltersBySubordinationOwner(t *testing.T) {
	f := newOwnerFixture(t)
	flt, _ := json.Marshal(map[string]string{metadata.StandardOwnerField: f.contractor.String()})
	rec := serveRefOptions(t, f.server, f.subordinate.Name, "limit=10&flt="+url.QueryEscape(string(flt)), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	labels := refOptionLabels(t, rec.Body.Bytes())
	if len(labels) != 2 {
		t.Fatalf("в подборе %d строк, ждали два договора нашего контрагента: %v", len(labels), labels)
	}
	for _, l := range labels {
		if l == "Договор чужого" {
			t.Fatalf("в подбор попал договор чужого контрагента: %v", labels)
		}
	}
}

// Владельца спросили, но не выбрали — список ПУСТ, а не полон: «сначала
// контрагент, потом договор». Полный список в ответ на невыбранного контрагента
// пользователь принимает за отсутствие отбора.
func TestRefOptionsEmptyWhenOwnerAskedButEmpty(t *testing.T) {
	f := newOwnerFixture(t)
	flt, _ := json.Marshal(map[string]string{metadata.StandardOwnerField: ""})
	rec := serveRefOptions(t, f.server, f.subordinate.Name, "limit=10&flt="+url.QueryEscape(string(flt)), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	if labels := refOptionLabels(t, rec.Body.Bytes()); len(labels) != 0 {
		t.Fatalf("владелец не выбран, а подбор вернул %v — ждали пусто", labels)
	}
}

// А вот если владельца на форме НЕ спрашивают (отбора в разметке нет), список
// показывается целиком — как в 1С, где подчинение без связи параметров выбора
// тоже показывает всё. Иначе поле на такой форме стало бы навсегда пустым.
func TestRefOptionsFullWhenOwnerNotAsked(t *testing.T) {
	f := newOwnerFixture(t)
	rec := serveRefOptions(t, f.server, f.subordinate.Name, "limit=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	if labels := refOptionLabels(t, rec.Body.Bytes()); len(labels) != 3 {
		t.Fatalf("без отбора подбор вернул %v, ждали все три договора", labels)
	}
}

// Справочник без подчинения ведёт себя как раньше: отбор не объявлен — видно всё.
func TestRefOptionsUnaffectedWithoutOwner(t *testing.T) {
	f := newOwnerFixture(t)
	rec := serveRefOptions(t, f.server, f.owner.Name, "limit=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	if labels := refOptionLabels(t, rec.Body.Bytes()); len(labels) != 2 {
		t.Fatalf("самостоятельный справочник вернул %v, ждали обоих контрагентов", labels)
	}
}

// Разметка поля несёт отбор: имя поля-источника (живое значение читает клиент) и
// значение на момент отрисовки (для источника, которого на форме нет).
func TestRefFilterMapFromSubordination(t *testing.T) {
	f := newOwnerFixture(t)
	values := map[string]string{"Контрагент": f.contractor.String()}
	got := f.server.refFilterMap(f.holder, nil, values)
	raw, ok := got["Договор"]
	if !ok {
		t.Fatalf("отбор для поля «Договор» не собран: %v", got)
	}
	var spec map[string]refFilterSource
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatal(err)
	}
	src := spec[metadata.StandardOwnerField]
	if src.From != "Контрагент" || src.Value != f.contractor.String() {
		t.Fatalf("источник отбора = %+v, ждали Контрагент с текущим значением", src)
	}
	if _, ok := got["Контрагент"]; ok {
		t.Fatal("у самостоятельного справочника отбора быть не должно")
	}
}

// Две ссылки на справочник-владелец — автоматика отказывается выбирать: молча
// взятое не то поле показало бы чужой список без объяснений. Для таких случаев
// есть явная связь параметров выбора.
func TestRefFilterSkipsAmbiguousOwnerSource(t *testing.T) {
	f := newOwnerFixture(t)
	f.holder.Fields = append(f.holder.Fields, metadata.Field{
		Name: "ПрежнийКонтрагент", Type: "reference:Контрагент", RefEntity: "Контрагент",
	})
	if got := f.server.refFilterMap(f.holder, nil, map[string]string{}); got["Договор"] != "" {
		t.Fatalf("при двух ссылках на владельца отбор собран автоматически: %v", got)
	}
}

// Явная связь параметров выбора (choice_filter) задаёт источник сама — в том
// числе реквизит формы, которого у объекта нет.
func TestRefFilterFromChoiceFilter(t *testing.T) {
	f := newOwnerFixture(t)
	form := &metadata.FormModule{
		Elements: []*metadata.FormElement{{
			Kind:         metadata.FormElementField,
			Name:         "ПолеДоговор",
			DataPath:     "Объект.Договор",
			ChoiceFilter: map[string]string{metadata.StandardOwnerField: "КонтрагентВыбор"},
		}},
	}
	values := map[string]string{"КонтрагентВыбор": f.other.String()}
	got := f.server.refFilterMap(f.holder, form, values)
	var spec map[string]refFilterSource
	if err := json.Unmarshal([]byte(got["Договор"]), &spec); err != nil {
		t.Fatalf("отбор не собран: %v (%v)", got, err)
	}
	src := spec[metadata.StandardOwnerField]
	if src.From != "КонтрагентВыбор" || src.Value != f.other.String() {
		t.Fatalf("явная связь не победила автоматику: %+v", src)
	}
}
