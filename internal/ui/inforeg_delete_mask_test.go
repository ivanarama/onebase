package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

	if rec.Code != http.StatusForbidden {
		t.Fatalf("удаление под маской: код %d, ожидался 403; body=%s", rec.Code, rec.Body.String())
	}

	// Lossless machine keys used by ordinary delete forms must not bypass the
	// field mask through a hidden input or another serialized template value.
	listReq := reqWithChi(http.MethodGet, "/ui/inforeg/"+ir.Name, nil,
		map[string]string{"name": ir.Name})
	listReq = listReq.WithContext(auth.ContextWithUser(listReq.Context(), user))
	listRec := httptest.NewRecorder()
	s.infoRegList(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("список под маской: код %d; body=%s", listRec.Code, listRec.Body.String())
	}
	body := listRec.Body.String()
	tableHTML := body
	if start := strings.Index(body, "<table>"); start >= 0 {
		tableHTML = body[start:]
	}
	// The reference filter above the table may legitimately contain the UUID;
	// the row/detail/delete payload below it must not.
	if strings.Contains(tableHTML, productID.String()) || strings.Contains(body, "onebase_info_reg_key_values") {
		t.Fatalf("машинный ключ замаскированного измерения попал в HTML: %s", body)
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

func TestUI_InfoRegDelete_НесуществующаяЗаписьНеСчитаетсяУдалённой(t *testing.T) {
	ir := &metadata.InfoRegister{
		Name:       "Настройки",
		Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeString}},
	}
	s, ctx := newSubmitTestServer(t, nil)
	if err := s.store.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	s.reg.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})

	req := reqWithChi(http.MethodPost, "/ui/inforeg/"+ir.Name+"/delete",
		url.Values{"Ключ": {"missing"}}, map[string]string{"name": ir.Name})
	rec := httptest.NewRecorder()
	s.infoRegDelete(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing row: status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
