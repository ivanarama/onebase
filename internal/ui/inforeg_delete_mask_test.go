package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Удаление записи регистра сведений под ролью с маской на ИЗМЕРЕНИИ обязано
// отказывать, а не молча «успешно» ничего не удалять (#861).
//
// Форма списка кладёт в hidden-поля то, что видит пользователь. Под маской это
// «••••••», и DELETE сравнивал маску с реальным значением: ни одной строки не
// находил и отвечал успехом. Пользователь считал запись удалённой, а она жива —
// худший исход из возможных: тихий, необратимый на вид и неверный.
func TestUI_InfoRegDelete_ПодМаскойНаИзмеренииОтказывает(t *testing.T) {
	goods := &metadata.Entity{
		Name:   "Товары",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	ir := &metadata.InfoRegister{
		Name: "Цены",
		Dimensions: []metadata.Field{
			{Name: "Товар", Type: "reference:Товары", RefEntity: goods.Name},
		},
		Resources: []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{goods})
	if err := s.store.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{goods}, InfoRegs: []*metadata.InfoRegister{ir}})

	productID := uuid.New()
	if err := s.store.Upsert(ctx, goods.Name, productID, map[string]any{"Наименование": "Стул"}, goods); err != nil {
		t.Fatal(err)
	}
	if err := s.store.InfoRegSet(ctx, ir,
		map[string]any{"Товар": productID.String()}, map[string]any{"Цена": 100}, nil); err != nil {
		t.Fatal(err)
	}

	user := &auth.User{ID: "masked", Login: "masked", Roles: []*auth.Role{{
		Name: "С маской на измерении",
		Permissions: auth.Permission{
			Catalogs: map[string][]string{goods.Name: {"read"}},
			InfoRegs: map[string][]string{ir.Name: {"read", "delete"}},
			FieldAccess: auth.FieldAccess{InfoRegs: map[string]auth.FieldPolicies{
				ir.Name: {"Товар": {Read: "mask_all"}},
			}},
		},
	}}}

	form := url.Values{"Товар": {"••••••"}}
	req := reqWithChi(http.MethodPost, "/ui/inforeg/"+ir.Name+"/delete", form,
		map[string]string{"name": ir.Name})
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	s.infoRegDelete(rec, req)

	if rec.Code >= 200 && rec.Code < 400 {
		t.Fatalf("удаление под маской завершилось «успехом» (код %d) — запись цела, а пользователь считает иначе", rec.Code)
	}

	// И запись действительно на месте: отказ обязан быть отказом, а не
	// частичным удалением.
	rows, err := s.store.InfoRegList(ctx, ir, storage.RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("строк осталось %d, ожидалась 1", len(rows))
	}
}
