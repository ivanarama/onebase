package ui

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// «Создать копированием» (issue #762): GET /ui/{kind}/{name}/new?copy=<id>
// открывает форму создания значениями существующей записи. Проверяем перенос
// значений, исключения (номер и дата документа, реквизиты под маской), права,
// иерархию и то, что копирование ничего не записывает.

func copyTestCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
			{Name: "Код", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{{Name: "Контакты", Fields: []metadata.Field{
			{Name: "Вид", Type: metadata.FieldTypeString},
			{Name: "Значение", Type: metadata.FieldTypeString},
		}}},
	}
}

func serveCopyForm(t *testing.T, s *Server, ctx context.Context, entity *metadata.Entity, query string) *httptest.ResponseRecorder {
	t.Helper()
	slug := strings.ToLower(entity.Name)
	req := httptest.NewRequest("GET", "/ui/"+strings.ToLower(string(entity.Kind))+"/"+slug+"/new?"+query, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", slug)
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.form(rec, req)
	return rec
}

func TestApplyCopyFromQuery_CopiesHeaderAndTableParts(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{
		"Наименование": "ООО Ромашка", "Телефон": "+7 900 111-22-33", "Код": "К-001",
	}, ent); err != nil {
		t.Fatal(err)
	}
	tpRows := []map[string]any{
		{"Вид": "рабочий", "Значение": "+7 495 000-00-00"},
		{"Вид": "личный", "Значение": "+7 916 000-00-00"},
	}
	if err := s.store.UpsertTablePartRows(ctx, ent.Name, "Контакты", srcID, tpRows, ent.TableParts[0]); err != nil {
		t.Fatal(err)
	}

	values := map[string]string{}
	tps := map[string][]map[string]any{}
	r := httptest.NewRequest("GET", "/ui/catalog/клиент/new?copy="+srcID.String(), nil)
	if errText := s.applyCopyFromQuery(r.WithContext(ctx), ent, srcID.String(), values, tps); errText != "" {
		t.Fatalf("applyCopyFromQuery: %s", errText)
	}

	if values["Наименование"] != "ООО Ромашка" || values["Телефон"] != "+7 900 111-22-33" {
		t.Errorf("шапка не скопирована: %#v", values)
	}
	// Код справочника платформа не генерирует — копия несёт его как есть.
	if values["Код"] != "К-001" {
		t.Errorf("Код = %q, ожидался перенос из источника", values["Код"])
	}
	if got := tps["Контакты"]; len(got) != 2 || got[0]["Значение"] != "+7 495 000-00-00" {
		t.Errorf("табличная часть не скопирована: %#v", got)
	}

	// Копирование ничего не пишет: в базе по-прежнему одна запись.
	rows, err := s.store.List(ctx, ent.Name, ent, storage.ListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("записей после копирования = %d, ожидалась 1 (копия живёт только в форме)", len(rows))
	}
}

func TestApplyCopyFromQuery_DocumentKeepsFreshNumberAndDate(t *testing.T) {
	doc := &metadata.Entity{
		Name: "Реализация", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Покупатель", Type: metadata.FieldTypeString},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, doc.Name, srcID, map[string]any{
		"Номер": "РТ-001", "Покупатель": "ООО Ромашка",
	}, doc); err != nil {
		t.Fatal(err)
	}

	// Дату форма подставляет до копирования — копия должна её сохранить.
	values := map[string]string{"Дата": "2026-08-12T10:00"}
	r := httptest.NewRequest("GET", "/ui/document/реализация/new?copy="+srcID.String(), nil)
	if errText := s.applyCopyFromQuery(r.WithContext(ctx), doc, srcID.String(), values, map[string][]map[string]any{}); errText != "" {
		t.Fatalf("applyCopyFromQuery: %s", errText)
	}
	if values["Номер"] != "" {
		t.Errorf("Номер = %q, копия должна получить номер при записи", values["Номер"])
	}
	if values["Дата"] != "2026-08-12T10:00" {
		t.Errorf("Дата = %q, ожидалась дата нового документа", values["Дата"])
	}
	if values["Покупатель"] != "ООО Ромашка" {
		t.Errorf("Покупатель = %q, ожидался перенос из источника", values["Покупатель"])
	}
}

func TestApplyCopyFromQuery_MaskedFieldIsNotCopied(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{
		"Наименование": "ООО Ромашка", "Телефон": "+7 900 111-22-33",
	}, ent); err != nil {
		t.Fatal(err)
	}

	user := uiMaskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})
	r := httptest.NewRequest("GET", "/ui/catalog/клиент/new?copy="+srcID.String(), nil)
	r = r.WithContext(auth.ContextWithUser(ctx, user))

	values := map[string]string{}
	if errText := s.applyCopyFromQuery(r, ent, srcID.String(), values, map[string][]map[string]any{}); errText != "" {
		t.Fatalf("applyCopyFromQuery: %s", errText)
	}
	if values["Наименование"] != "ООО Ромашка" {
		t.Errorf("незащищённый реквизит не скопирован: %#v", values)
	}
	if v, ok := values["Телефон"]; ok {
		t.Errorf("Телефон = %q: закрытый маской реквизит в копию не переносится — иначе в новую запись уедет строка-маска", v)
	}
}

func TestApplyCopyFromQuery_RequiresReadOnSource(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{"Наименование": "ООО Ромашка"}, ent); err != nil {
		t.Fatal(err)
	}

	user := &auth.User{Login: "чужой", Roles: []*auth.Role{{
		Permissions: auth.Permission{Catalogs: map[string][]string{"Другое": {"read"}}},
	}}}
	r := httptest.NewRequest("GET", "/ui/catalog/клиент/new?copy="+srcID.String(), nil)
	r = r.WithContext(auth.ContextWithUser(ctx, user))

	values := map[string]string{}
	if errText := s.applyCopyFromQuery(r, ent, srcID.String(), values, map[string][]map[string]any{}); errText == "" {
		t.Fatal("копирование без права чтения источника должно возвращать ошибку")
	}
	if len(values) != 0 {
		t.Errorf("значения источника утекли в форму: %#v", values)
	}
}

func TestApplyCopyFromQuery_BadIDAndMissingRow(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	r := httptest.NewRequest("GET", "/ui/catalog/клиент/new", nil).WithContext(ctx)

	if errText := s.applyCopyFromQuery(r, ent, "не-uuid", map[string]string{}, map[string][]map[string]any{}); errText == "" {
		t.Error("некорректный идентификатор должен давать ошибку")
	}
	values := map[string]string{}
	if errText := s.applyCopyFromQuery(r, ent, uuid.New().String(), values, map[string][]map[string]any{}); errText == "" {
		t.Error("несуществующая запись должна давать ошибку")
	}
	if len(values) != 0 {
		t.Errorf("значения не должны появляться при ошибке: %#v", values)
	}
}

// Копия группы остаётся группой и в той же родительской группе.
func TestApplyCopyFromQuery_HierarchyFollowsSource(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Папки", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	parentID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, parentID, map[string]any{"Наименование": "Родитель", "is_folder": true}, ent); err != nil {
		t.Fatal(err)
	}
	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{
		"Наименование": "Вложенная группа", "is_folder": true, "parent_id": parentID.String(),
	}, ent); err != nil {
		t.Fatal(err)
	}

	values := map[string]string{}
	r := httptest.NewRequest("GET", "/ui/catalog/папки/new?copy="+srcID.String(), nil).WithContext(ctx)
	if errText := s.applyCopyFromQuery(r, ent, srcID.String(), values, map[string][]map[string]any{}); errText != "" {
		t.Fatalf("applyCopyFromQuery: %s", errText)
	}
	if values["is_folder"] != "true" {
		t.Errorf("is_folder = %q, копия группы должна остаться группой", values["is_folder"])
	}
	if values["parent_id"] != parentID.String() {
		t.Errorf("parent_id = %q, ожидалась группа оригинала %s", values["parent_id"], parentID)
	}
}

// Сквозная проверка обработчика: ?copy= доезжает до HTML формы создания,
// запись при этом не создаётся.
func TestForm_CopyQueryRendersFilledCreateForm(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{
		"Наименование": "ООО Ромашка", "Телефон": "+7 900 111-22-33",
	}, ent); err != nil {
		t.Fatal(err)
	}

	rec := serveCopyForm(t, s, ctx, ent, "copy="+srcID.String())
	if rec.Code != 200 {
		t.Fatalf("code = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="ООО Ромашка"`) {
		t.Error("значение источника не попало в форму создания")
	}
	// «+» шаблон отдаёт как &#43;, поэтому сравниваем по остатку номера.
	if !strings.Contains(body, "7 900 111-22-33") {
		t.Error("второй реквизит источника не попал в форму создания")
	}

	rows, err := s.store.List(ctx, ent.Name, ent, storage.ListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("записей = %d, копирование не должно ничего записывать", len(rows))
	}
}

// Список отдаёт строке ссылку копирования, а карточка — кнопку «Скопировать».
func TestListAndCardOfferCopy(t *testing.T) {
	ent := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	srcID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, srcID, map[string]any{"Наименование": "ООО Ромашка"}, ent); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/ui/catalog/клиент", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", "клиент")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.list(rec, req)
	if rec.Code != 200 {
		t.Fatalf("список: code = %d, body: %s", rec.Code, rec.Body.String())
	}
	// Имя сущности шаблон отдаёт percent-encoded, поэтому сверяем хвост ссылки.
	if !strings.Contains(rec.Body.String(), "data-copy-url=") ||
		!strings.Contains(rec.Body.String(), "/new?copy="+srcID.String()) {
		t.Error("строка списка не несёт ссылку копирования (пункт меню и F9 без неё не работают)")
	}

	card := httptest.NewRequest("GET", "/ui/catalog/клиент/"+srcID.String(), nil)
	cardCtx := chi.NewRouteContext()
	cardCtx.URLParams.Add("entity", "клиент")
	cardCtx.URLParams.Add("id", srcID.String())
	card = card.WithContext(context.WithValue(ctx, chi.RouteCtxKey, cardCtx))
	cardRec := httptest.NewRecorder()
	s.formEdit(cardRec, card)
	if cardRec.Code != 200 {
		t.Fatalf("карточка: code = %d, body: %s", cardRec.Code, cardRec.Body.String())
	}
	if !strings.Contains(cardRec.Body.String(), "/new?copy="+srcID.String()) {
		t.Error("на карточке нет кнопки «Скопировать»")
	}
}
