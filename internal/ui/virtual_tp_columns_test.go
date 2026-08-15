package ui

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

// Виртуальная колонка ТЧ (#845): реквизит по ссылке из строки показывается, но
// не хранится. Проверяем ровно тем путём, которым её видит пользователь —
// GET карточки документа, а не вызовом хелпера напрямую.
func virtualColumnFixture(t *testing.T) (*Server, *metadata.Entity, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "vc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	client := &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Код", Type: metadata.FieldTypeString},
		},
	}
	order := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{
				{Name: "Клиент", Type: metadata.FieldType("reference:Клиент"), RefEntity: "Клиент"},
				{Name: "Сумма", Type: metadata.FieldTypeNumber},
			},
		}},
	}
	order.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: order.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки",
			VirtualColumns: []metadata.FormVirtualColumn{{
				Name: "КодКлиента", DataPath: "Клиент.Код",
				TitleMap: map[string]string{"ru": "Код клиента"}, Width: 90,
			}},
		}},
	}}

	if err := db.Migrate(ctx, []*metadata.Entity{client, order}); err != nil {
		t.Fatal(err)
	}
	clientID := uuid.New()
	if err := db.Upsert(ctx, client.Name, clientID,
		map[string]any{"Наименование": "ООО Ромашка", "Код": "К-000042"}, client); err != nil {
		t.Fatal(err)
	}
	orderID := uuid.New()
	if err := db.Upsert(ctx, order.Name, orderID, map[string]any{"Дата": time.Now()}, order); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTablePartRows(ctx, order.Name, "Строки", orderID, []map[string]any{
		{"Клиент": clientID.String(), "Сумма": 100},
		{"Клиент": nil, "Сумма": 200}, // строка без ссылки — рабочее состояние ввода
	}, order.TableParts[0]); err != nil {
		t.Fatal(err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{client, order}})
	s := &Server{
		store:       db,
		reg:         reg,
		messages:    NewMessageStore(),
		widgetCache: widget.NewCache(time.Minute),
	}
	return s, order, orderID
}

func virtualColumnFormHTML(t *testing.T, s *Server, entity *metadata.Entity, id uuid.UUID) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/ui/document/"+entity.Name+"/"+id.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "document")
	rctx.URLParams.Add("entity", entity.Name)
	rctx.URLParams.Add("id", id.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{Login: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()
	s.formEdit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("карточка документа вернула %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestVirtualTPColumn_ЗначениеИКолонкаНаФорме(t *testing.T) {
	s, order, orderID := virtualColumnFixture(t)
	html := virtualColumnFormHTML(t, s, order, orderID)

	cols := parseManagedTPColumns(t, html)
	var virtual *managedTPColumnJSON
	for i := range cols {
		if cols[i].ID == "КодКлиента" {
			virtual = &cols[i]
		}
	}
	if virtual == nil {
		t.Fatalf("виртуальной колонки нет в data-sg-cols: %+v", cols)
	}
	if !virtual.Virtual {
		t.Error("колонка не помечена virtual — клиент разрешит её редактировать и отправит на запись")
	}
	if virtual.Name != "Код клиента" {
		t.Errorf("подпись колонки = %q, ожидалась «Код клиента»", virtual.Name)
	}
	// Хранимые колонки идут первыми и в прежнем порядке.
	if len(cols) != 3 || cols[0].ID != "Клиент" || cols[1].ID != "Сумма" {
		t.Errorf("порядок колонок изменился: %+v", cols)
	}

	rows := parseManagedTPRows(t, html)
	if len(rows) != 2 {
		t.Fatalf("строк ТЧ %d, ожидалось 2", len(rows))
	}
	if got := rows[0]["КодКлиента"]; got != "К-000042" {
		t.Errorf("значение виртуальной колонки = %v, ожидалось К-000042", got)
	}
	// Пустая ссылка — пустая ячейка, без маркера: «—» читался бы как значение.
	if got := rows[1]["КодКлиента"]; got != "" {
		t.Errorf("строка без ссылки получила значение %v, ожидалась пустая ячейка", got)
	}
}

// Гарантия «не пишется»: подделанный payload с именем виртуальной колонки не
// доезжает до табличной части. Клиент её не отправляет, но проверять надо
// сервер — на него приходит что угодно.
func TestVirtualTPColumn_ПодделанныйPayloadНеПишется(t *testing.T) {
	_, order, orderID := virtualColumnFixture(t)
	form := order.Forms[0]

	body := strings.NewReader(
		`tp_json.Строки=[{"Клиент":"","Сумма":"5","КодКлиента":"ПОДДЕЛКА"}]`)
	req := httptest.NewRequest(http.MethodPost, "/ui/document/Заказ/"+orderID.String(), body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatal(err)
	}
	rows, err := parseTablePartRowsForManagedForm(req, order, form, true)
	if err != nil {
		t.Fatalf("разбор payload: %v", err)
	}
	if len(rows["Строки"]) != 1 {
		t.Fatalf("строк %d, ожидалась 1", len(rows["Строки"]))
	}
	for key := range rows["Строки"][0] {
		if strings.EqualFold(key, "КодКлиента") {
			t.Fatalf("виртуальная колонка доехала до записи: %#v", rows["Строки"][0])
		}
	}
}

// parseManagedTPRows достаёт строки ТЧ из data-sg-rows отрисованной формы.
func parseManagedTPRows(t *testing.T, htmlOut string) []map[string]any {
	t.Helper()
	const prefix = `data-sg-rows='`
	start := strings.Index(htmlOut, prefix)
	if start < 0 {
		t.Fatal("data-sg-rows не найден")
	}
	start += len(prefix)
	end := strings.Index(htmlOut[start:], `'`)
	if end < 0 {
		t.Fatal("data-sg-rows не закрыт")
	}
	raw := html.UnescapeString(htmlOut[start : start+end])
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("data-sg-rows невалиден (%q): %v", raw, err)
	}
	return rows
}

// Доступ к целевому объекту проверяется отдельно от подписи. Подпись чужой
// записи UI отдаёт и сегодня, но виртуальная колонка показывает ПРОИЗВОЛЬНЫЙ
// реквизит — расширять на него исторический зазор нельзя. Строка, закрытая
// строковой политикой, даёт пустую ячейку.
func TestVirtualTPColumn_СтрокаЗакрытаяПолитикойНеПоказывается(t *testing.T) {
	s, order, orderID := virtualColumnFixture(t)

	restricted := &auth.User{Login: "u", Roles: []*auth.Role{{
		Permissions: auth.Permission{
			Documents: map[string][]string{"Заказ": {"read", "write"}},
			Catalogs:  map[string][]string{"Клиент": {"read"}},
			RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
				"Клиент": {"read": {Field: "Код", Op: "eq", Value: auth.RowValue{Literal: "другой-код"}}},
			}},
		},
	}}}
	req := httptest.NewRequest(http.MethodGet, "/ui/document/Заказ/"+orderID.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "document")
	rctx.URLParams.Add("entity", order.Name)
	rctx.URLParams.Add("id", orderID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(auth.ContextWithUser(req.Context(), restricted))
	rec := httptest.NewRecorder()
	s.formEdit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("карточка вернула %d: %s", rec.Code, rec.Body.String())
	}
	rows := parseManagedTPRows(t, rec.Body.String())
	if len(rows) == 0 {
		t.Fatal("строки ТЧ не отрисованы")
	}
	if got := rows[0]["КодКлиента"]; got != "" {
		t.Fatalf("реквизит недоступной строки показан: %v", got)
	}
}

func TestVirtualTPColumn_NoGridPublishesVirtualSchema(t *testing.T) {
	s, order, orderID := virtualColumnFixture(t)
	order.Forms[0].Elements[0].NoGrid = true
	htmlOut := virtualColumnFormHTML(t, s, order, orderID)

	const prefix = `data-tp-virtual-cols="`
	start := strings.Index(htmlOut, prefix)
	if start < 0 {
		t.Fatal("no_grid tbody не содержит схему виртуальных колонок")
	}
	start += len(prefix)
	end := strings.Index(htmlOut[start:], `"`)
	if end < 0 {
		t.Fatal("атрибут data-tp-virtual-cols не закрыт")
	}
	var names []string
	if err := json.Unmarshal([]byte(html.UnescapeString(htmlOut[start:start+end])), &names); err != nil {
		t.Fatalf("data-tp-virtual-cols содержит невалидный JSON: %v", err)
	}
	if len(names) != 1 || names[0] != "КодКлиента" {
		t.Fatalf("неверная схема виртуальных колонок: %#v", names)
	}
	if !strings.Contains(htmlOut, `data-ob-virtual-col="КодКлиента">К-000042</td>`) {
		t.Fatal("начальный no_grid render потерял значение виртуальной колонки")
	}
}

func TestFormElementTablePartRequiresTablePartKind(t *testing.T) {
	entity := &metadata.Entity{TableParts: []metadata.TablePart{{Name: "Строки"}}}
	el := &metadata.FormElement{
		Kind: metadata.FormElementField, DataPath: "Объект.Строки",
	}
	if got := formElementTablePart(entity, el); got != nil {
		t.Fatalf("элемент kind=%q принят как табличная часть: %+v", el.Kind, got)
	}
}

func TestApplyVirtualTPColumns_SkipsReservedNames(t *testing.T) {
	s, order, orderID := virtualColumnFixture(t)
	loaded := parseManagedTPRows(t, virtualColumnFormHTML(t, s, order, orderID))
	rows := map[string][]map[string]any{
		"Строки": {{"Клиент": loaded[0]["Клиент"]}},
	}
	form := &metadata.FormModule{Elements: []*metadata.FormElement{{
		Kind: metadata.FormElementTablePart, DataPath: "Объект.Строки",
		VirtualColumns: []metadata.FormVirtualColumn{{Name: "_OrD", DataPath: "Клиент.Код"}},
	}}}
	s.applyVirtualTPColumns(context.Background(), order, form, rows)
	if _, exists := rows["Строки"][0]["_OrD"]; exists {
		t.Fatalf("runtime материализовал служебную колонку: %#v", rows["Строки"][0])
	}
}

func TestApplyVirtualTPColumns_FirstValidBindingWinsAcrossViews(t *testing.T) {
	s, order, orderID := virtualColumnFixture(t)
	loaded := parseManagedTPRows(t, virtualColumnFormHTML(t, s, order, orderID))
	rows := map[string][]map[string]any{
		"Строки": {{"Клиент": loaded[0]["Клиент"]}},
	}
	form := &metadata.FormModule{Elements: []*metadata.FormElement{
		{
			Kind: metadata.FormElementTablePart, Name: "НекорректныйВид", DataPath: "Объект.Строки",
			VirtualColumns: []metadata.FormVirtualColumn{{Name: "Проекция", DataPath: "Нет.Код"}},
		},
		{
			Kind: metadata.FormElementTablePart, Name: "ОсновнойВид", DataPath: "Объект.Строки",
			VirtualColumns: []metadata.FormVirtualColumn{{Name: "Проекция", DataPath: "Клиент.Код"}},
		},
		{
			Kind: metadata.FormElementTablePart, Name: "КонфликтующийВид", DataPath: "Объект.Строки",
			VirtualColumns: []metadata.FormVirtualColumn{{Name: "Проекция", DataPath: "Клиент.Наименование"}},
		},
	}}
	s.applyVirtualTPColumns(context.Background(), order, form, rows)
	if got := rows["Строки"][0]["Проекция"]; got != "К-000042" {
		t.Fatalf("последующее представление перезаписало первый валидный binding: %v", got)
	}
}
