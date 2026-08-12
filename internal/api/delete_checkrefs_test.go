package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// DATA-01 / issue #774: удаление через публичный REST-путь не должно оставлять
// висячие ссылки. Раньше CheckRefs звал только UI-обработчик, а DELETE по REST
// v1 удалял объект, на который ссылается табличная часть другого документа (FK
// для полей ТЧ не создаётся), — строка ТЧ оставалась указывать в никуда.
// Предохранитель теперь живёт внутри entityservice.Delete, поэтому REST обязан
// вернуть 409 и сохранить объект. Проверяем через реальный обработчик маршрута.
func TestAPI_Delete_BlockedByTablePartReference(t *testing.T) {
	client := &metadata.Entity{
		Name:   "Контрагент",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	order := &metadata.Entity{
		Name:   "Заказ",
		Kind:   metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{
			{Name: "Строки", Fields: []metadata.Field{
				{Name: "Контрагент", Type: metadata.FieldType("reference:Контрагент"), RefEntity: client.Name},
			}},
		},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{client, order}, nil)

	clientID := uuid.New()
	if err := h.store.Upsert(ctx, "Контрагент", clientID, map[string]any{"Наименование": "ООО Ромашка"}, client); err != nil {
		t.Fatal(err)
	}

	// Заказ со строкой ТЧ, ссылающейся на контрагента.
	body := []byte(`{"Номер":"1","__tableparts":{"Строки":[{"Контрагент":"` + clientID.String() + `"}]}}`)
	cr := reqWithEntity("POST", "/documents/Заказ", body, map[string]string{"entity": "Заказ"}, nil)
	cw := httptest.NewRecorder()
	h.createObject(metadata.KindDocument).ServeHTTP(cw, cr)
	if cw.Code != http.StatusCreated {
		t.Fatalf("создание заказа: ожидался 201, получено %d: %s", cw.Code, cw.Body.String())
	}

	// Удаление ссылаемого контрагента через REST должно быть отклонено.
	dr := reqWithEntity("DELETE", "/catalogs/Контрагент", nil,
		map[string]string{"entity": "Контрагент", "id": clientID.String()}, nil)
	dw := httptest.NewRecorder()
	h.deleteObject(metadata.KindCatalog).ServeHTTP(dw, dr)
	if dw.Code != http.StatusConflict {
		t.Fatalf("удаление ссылаемого контрагента: ожидался 409, получено %d: %s", dw.Code, dw.Body.String())
	}

	// Контрагент обязан уцелеть — иначе ссылка из ТЧ осиротела.
	if _, err := h.store.GetByID(ctx, "Контрагент", clientID, client); err != nil {
		t.Fatalf("контрагент удалён несмотря на 409 — ссылочная целостность нарушена: %v", err)
	}
}
