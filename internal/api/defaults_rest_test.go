package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// REST — третий путь создания объекта. Значения по умолчанию (план 153)
// обязаны применяться и здесь: документ, созданный интеграцией, не должен
// отличаться от созданного руками.

func restDefaultsEntities() []*metadata.Entity {
	org := &metadata.Entity{
		Name: "Организация",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	doc := &metadata.Entity{
		Name: "Реализация",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Организация", Type: "reference:Организация", RefEntity: "Организация", Default: "единственный"},
			{Name: "Комментарий", Type: metadata.FieldTypeString, Default: "по умолчанию"},
		},
	}
	return []*metadata.Entity{org, doc}
}

func createViaREST(t *testing.T, h *handler, body string) uuid.UUID {
	t.Helper()
	r := reqWithEntity("POST", "/documents/Реализация", []byte(body), map[string]string{"entity": "Реализация"}, nil)
	w := httptest.NewRecorder()
	h.createObject(metadata.KindDocument).ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("ожидался 201, получен %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	id, err := uuid.Parse(resp.ID)
	if err != nil {
		t.Fatalf("некорректный id в ответе: %v", err)
	}
	return id
}

func TestAPI_Create_AppliesFieldDefaults(t *testing.T) {
	ents := restDefaultsEntities()
	h, ctx := newAPITestHandler(t, ents, nil)

	orgID := uuid.New()
	if err := h.store.Upsert(ctx, "Организация", orgID, map[string]any{"Наименование": "ООО Ромашка"}, ents[0]); err != nil {
		t.Fatal(err)
	}

	id := createViaREST(t, h, `{"Номер":"1"}`)
	row, err := h.store.GetByID(ctx, "Реализация", id, ents[1])
	if err != nil {
		t.Fatal(err)
	}
	if got := refUUIDString(row["Организация"]); got != orgID.String() {
		t.Errorf("Организация = %v, ожидалась единственная %s", row["Организация"], orgID)
	}
	if row["Комментарий"] != "по умолчанию" {
		t.Errorf("Комментарий = %v", row["Комментарий"])
	}
}

// Значение из тела запроса главнее дефолта — иначе интеграция не смогла бы
// передать пустой комментарий осознанно.
func TestAPI_Create_BodyWinsOverDefault(t *testing.T) {
	ents := restDefaultsEntities()
	h, ctx := newAPITestHandler(t, ents, nil)
	orgID := uuid.New()
	if err := h.store.Upsert(ctx, "Организация", orgID, map[string]any{"Наименование": "ООО Ромашка"}, ents[0]); err != nil {
		t.Fatal(err)
	}

	id := createViaREST(t, h, `{"Номер":"1","Комментарий":"из запроса"}`)
	row, err := h.store.GetByID(ctx, "Реализация", id, ents[1])
	if err != nil {
		t.Fatal(err)
	}
	if row["Комментарий"] != "из запроса" {
		t.Errorf("Комментарий = %v, ожидалось значение из тела запроса", row["Комментарий"])
	}
}

// Второй элемент справочника отменяет подстановку и на REST-пути: поведение
// источника `единственный` одинаково во всех трёх входах.
func TestAPI_Create_SingleDefaultSkippedWhenAmbiguous(t *testing.T) {
	ents := restDefaultsEntities()
	h, ctx := newAPITestHandler(t, ents, nil)
	for _, name := range []string{"ООО Ромашка", "ИП Иванов"} {
		if err := h.store.Upsert(ctx, "Организация", uuid.New(), map[string]any{"Наименование": name}, ents[0]); err != nil {
			t.Fatal(err)
		}
	}

	id := createViaREST(t, h, `{"Номер":"1"}`)
	row, err := h.store.GetByID(ctx, "Реализация", id, ents[1])
	if err != nil {
		t.Fatal(err)
	}
	if got := refUUIDString(row["Организация"]); got != "" {
		t.Errorf("Организация = %v, ожидалось пусто при двух элементах", row["Организация"])
	}
}

func refUUIDString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case uuid.UUID:
		if t == uuid.Nil {
			return ""
		}
		return t.String()
	case []byte:
		if id, err := uuid.FromBytes(t); err == nil {
			return id.String()
		}
	}
	return ""
}

// Строковый доступ учитывается источником `единственный`: подставиться может
// только то, что пользователь видит в списке. Здесь в справочнике две
// организации, но пользователю видна одна — она и попадает в документ.
//
// Это же и обратная сторона источника, ради которой он сделан опт-ином: у
// разных пользователей одна и та же кнопка «Создать» даёт разный документ.
func TestAPI_Create_SingleDefaultRespectsRowAccess(t *testing.T) {
	ents := restDefaultsEntities()
	ents[0].Fields = append(ents[0].Fields, metadata.Field{Name: "Owner", Type: metadata.FieldTypeString})
	h, ctx := newAPITestHandler(t, ents, nil)

	mine := uuid.New()
	if err := h.store.Upsert(ctx, "Организация", mine, map[string]any{"Наименование": "Моя", "Owner": "u"}, ents[0]); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Upsert(ctx, "Организация", uuid.New(), map[string]any{"Наименование": "Чужая", "Owner": "other"}, ents[0]); err != nil {
		t.Fatal(err)
	}

	user := apiUser("u", auth.Permission{
		Catalogs:  map[string][]string{"Организация": {"read"}},
		Documents: map[string][]string{"Реализация": {"read", "write"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			"Организация": {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}},
		}},
	})
	req := withUser(reqWithEntity(http.MethodPost, "/documents/Реализация", []byte(`{"Номер":"1"}`), map[string]string{"entity": "Реализация"}, nil), user)
	rec := httptest.NewRecorder()
	h.createObject(metadata.KindDocument)(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var res map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse(res["id"])
	row, err := h.store.GetByID(ctx, "Реализация", id, ents[1])
	if err != nil {
		t.Fatal(err)
	}
	if got := refUUIDString(row["Организация"]); got != mine.String() {
		t.Errorf("Организация = %v, ожидалась видимая пользователю %s", row["Организация"], mine)
	}
}
