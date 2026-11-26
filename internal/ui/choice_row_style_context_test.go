package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// Оформление строк выбора от формы и от данных, как в форме звонка 1С:
//   - бренды подкрашиваются цветом из своей карточки (ЦветФона.Цвет,
//     ЦветТекстаВСписке.Цвет), но только когда у направления звонка стоит
//     флаг — флаг формы приходит через choice_style_params поля;
//   - архивная реклама (группа «Архивные», на любой глубине) — оранжево-
//     красная и недоступна для выбора.
// Проверяется публичный маршрут подбора и отрисовка формы: браузер присылает
// только ссылку направления, флаг за ней читает сервер.

type styleContextFixture struct {
	server                        *Server
	call, brands, ads             *metadata.Entity
	paint, plainDir, hidden       uuid.UUID
	red, green, broken, noColor   uuid.UUID
	archived, nested, current     uuid.UUID
	archiveGroup, archiveSubgroup uuid.UUID
	callID, archivedCallID        uuid.UUID
}

func newStyleContextFixture(t *testing.T) styleContextFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "style-context.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	colors := &metadata.Entity{Name: "Цвета", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Цвет", Type: metadata.FieldTypeString},
	}}
	brands := &metadata.Entity{Name: "Бренды", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "ЦветФона", Type: metadata.FieldType("reference:Цвета"), RefEntity: "Цвета"},
		{Name: "ЦветТекстаВСписке", Type: metadata.FieldType("reference:Цвета"), RefEntity: "Цвета"},
	}, ChoiceRowStyle: &metadata.ChoiceRowStyle{Rules: []metadata.ChoiceStyleRule{
		{WhenParam: "ФонБренда", BackgroundFrom: "ЦветФона.Цвет"},
		{WhenParam: "ЦветБренда", ColorFrom: "ЦветТекстаВСписке.Цвет"},
	}}}
	directions := &metadata.Entity{Name: "Направления", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "ПодкрашиватьФонБренда", Type: metadata.FieldTypeBool},
		{Name: "ПодкрашиватьБренды", Type: metadata.FieldTypeBool},
		{Name: "Аудитория", Type: metadata.FieldTypeString},
	}}
	ads := &metadata.Entity{Name: "Реклама", Kind: metadata.KindCatalog, Hierarchical: true, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
	}, ChoiceRowStyle: &metadata.ChoiceRowStyle{Rules: []metadata.ChoiceStyleRule{
		{WhenInFolder: "Архивные", Style: metadata.FormCellStyle{Color: "#FF4500"}, Disabled: true},
	}}}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", EntityName: "Звонок", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{ID: "direction", Name: "ПолеНаправление", Kind: metadata.FormElementField, DataPath: "Объект.Направление"},
			{ID: "brand-picker", Name: "ПолеБренд", Kind: metadata.FormElementField, DataPath: "Объект.Бренд",
				ChoiceStyleParams: map[string]string{
					"ФонБренда":  "Объект.Направление.ПодкрашиватьФонБренда",
					"ЦветБренда": "Объект.Направление.ПодкрашиватьБренды",
				}},
			{ID: "ad-picker", Name: "ПолеРеклама", Kind: metadata.FormElementField, DataPath: "Объект.Реклама"},
		},
	}
	call := &metadata.Entity{Name: "Звонок", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Направление", Type: metadata.FieldType("reference:Направления"), RefEntity: "Направления"},
		{Name: "Бренд", Type: metadata.FieldType("reference:Бренды"), RefEntity: "Бренды"},
		{Name: "Реклама", Type: metadata.FieldType("reference:Реклама"), RefEntity: "Реклама"},
	}, Forms: []*metadata.FormModule{form}}
	entities := []*metadata.Entity{colors, brands, directions, ads, call}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}

	f := styleContextFixture{call: call, brands: brands, ads: ads,
		paint: uuid.New(), plainDir: uuid.New(), hidden: uuid.New(),
		red: uuid.New(), green: uuid.New(), broken: uuid.New(), noColor: uuid.New(),
		archiveGroup: uuid.New(), archiveSubgroup: uuid.New(),
		archived: uuid.New(), nested: uuid.New(), current: uuid.New(), callID: uuid.New(), archivedCallID: uuid.New(),
	}
	upsert := func(e *metadata.Entity, id uuid.UUID, fields map[string]any) {
		t.Helper()
		if err := db.Upsert(ctx, e.Name, id, fields, e); err != nil {
			t.Fatalf("seed %s: %v", e.Name, err)
		}
	}
	redColor, greenColor, badColor, textColor := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	upsert(colors, redColor, map[string]any{"Наименование": "Красный", "Цвет": "#FFC0C0"})
	upsert(colors, greenColor, map[string]any{"Наименование": "Зелёный", "Цвет": "rgb(192,255,192)"})
	upsert(colors, badColor, map[string]any{"Наименование": "Битый", "Цвет": "red;background:url(x)"})
	upsert(colors, textColor, map[string]any{"Наименование": "Синий", "Цвет": "#0000FF"})
	upsert(brands, f.red, map[string]any{"Наименование": "Атлант", "ЦветФона": redColor.String(), "ЦветТекстаВСписке": textColor.String()})
	upsert(brands, f.green, map[string]any{"Наименование": "Бош", "ЦветФона": greenColor.String()})
	upsert(brands, f.broken, map[string]any{"Наименование": "Канди", "ЦветФона": badColor.String()})
	upsert(brands, f.noColor, map[string]any{"Наименование": "Электролюкс"})
	upsert(directions, f.paint, map[string]any{"Наименование": "Холодильники", "ПодкрашиватьФонБренда": true, "Аудитория": "anna"})
	upsert(directions, f.plainDir, map[string]any{"Наименование": "Плиты", "ПодкрашиватьФонБренда": false, "Аудитория": "anna"})
	// Флаг стоит, но направление закрыто от оператора строковым доступом:
	// подкраска не должна выдавать, что флаг там стоит.
	upsert(directions, f.hidden, map[string]any{"Наименование": "Чужое", "ПодкрашиватьФонБренда": true, "Аудитория": "bob"})
	upsert(ads, f.archiveGroup, map[string]any{"Наименование": "Архивные", "ЭтоГруппа": true})
	upsert(ads, f.archiveSubgroup, map[string]any{"Наименование": "2025", "ЭтоГруппа": true, "Родитель": f.archiveGroup.String()})
	upsert(ads, f.archived, map[string]any{"Наименование": "Листовки", "Родитель": f.archiveGroup.String()})
	upsert(ads, f.nested, map[string]any{"Наименование": "Радио 2025", "Родитель": f.archiveSubgroup.String()})
	upsert(ads, f.current, map[string]any{"Наименование": "Сайт"})
	upsert(call, f.callID, map[string]any{"Направление": f.paint.String()})
	upsert(call, f.archivedCallID, map[string]any{"Направление": f.paint.String(), "Реклама": f.nested.String()})

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	f.server = &Server{reg: reg, store: db}
	f.server.entitySvc = f.server.newEntityService(nil)
	return f
}

func styleContextUser() *auth.User {
	return &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs:  map[string][]string{"Бренды": {"read"}, "Цвета": {"read"}, "Направления": {"read"}, "Реклама": {"read"}},
		Documents: map[string][]string{"Звонок": {"read", "write"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			"Направления": {"read": {Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}}},
		}},
	}}}}
}

type styledOption struct {
	style    string
	disabled bool
}

func (f styleContextFixture) options(t *testing.T, entity string, query url.Values) map[uuid.UUID]styledOption {
	t.Helper()
	query.Set("limit", "100")
	router := chi.NewRouter()
	f.server.Mount(router)
	req := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(entity)+"?"+query.Encode(), nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), styleContextUser()))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	out := map[uuid.UUID]styledOption{}
	for _, it := range got.Items {
		id, err := uuid.Parse(refValueString(it["id"]))
		if err != nil {
			continue
		}
		var o styledOption
		o.style, _ = it[refOptionStyleKey].(string)
		if bg, _ := it[refOptionStyleBgKey].(string); bg != "" {
			o.style += "|" + bg
		}
		o.disabled, _ = it[refOptionDisabledKey].(bool)
		out[id] = o
	}
	return out
}

func (f styleContextFixture) brandOptions(t *testing.T, direction string) map[uuid.UUID]styledOption {
	t.Helper()
	sources, _ := json.Marshal(map[string]string{
		"Объект.Направление.ПодкрашиватьФонБренда": direction,
		"Объект.Направление.ПодкрашиватьБренды":    direction,
	})
	return f.options(t, f.brands.Name, url.Values{
		"form_entity": {f.call.Name}, "form": {"ФормаОбъекта"}, "element": {"brand-picker"}, "sources": {string(sources)},
	})
}

func TestChoiceRowStyle_BrandBackgroundFromDirectionFlag(t *testing.T) {
	f := newStyleContextFixture(t)

	got := f.brandOptions(t, f.paint.String())
	if s := got[f.red].style; !strings.Contains(strings.ToLower(s), "background:#ffc0c0") {
		t.Errorf("направление с флагом: фон бренда из карточки, получено %q", s)
	}
	if s := got[f.green].style; !strings.Contains(s, "background:rgb(192,255,192)") {
		t.Errorf("направление с флагом: фон rgb(), получено %q", s)
	}
	if s := got[f.broken].style; s != "" {
		t.Errorf("недопустимый цвет из карточки не должен попасть в CSS: %q", s)
	}
	if s := got[f.noColor].style; s != "" {
		t.Errorf("бренд без цвета — без оформления: %q", s)
	}
	// Флаг цвета текста у направления не стоит — синего текста нет.
	if s := got[f.red].style; strings.Contains(strings.ToLower(s), "#0000ff") {
		t.Errorf("цвет текста без флага направления: %q", s)
	}

	for name, dir := range map[string]string{
		"флаг снят": f.plainDir.String(),
		"направление закрыто строк. доступом": f.hidden.String(),
		"направление не выбрано":              "",
		"чужой текст вместо ссылки":           "Истина",
	} {
		for id, o := range f.brandOptions(t, dir) {
			if o.style != "" || o.disabled {
				t.Errorf("%s: у бренда %s оформление %+v", name, id, o)
			}
		}
	}

	// Без контекста формы (обычный подбор) флагов нет — и подкраски нет.
	for id, o := range f.options(t, f.brands.Name, url.Values{}) {
		if o.style != "" {
			t.Errorf("подбор без формы: у бренда %s стиль %q", id, o.style)
		}
	}
}

// Источник флага, не объявленный в choice_style_params, отвергается: клиент не
// может подсунуть произвольный путь.
func TestChoiceRowStyle_UnknownStyleSourceRejected(t *testing.T) {
	f := newStyleContextFixture(t)
	sources, _ := json.Marshal(map[string]string{"Объект.Направление.Аудитория": f.paint.String()})
	router := chi.NewRouter()
	f.server.Mount(router)
	query := url.Values{"form_entity": {f.call.Name}, "form": {"ФормаОбъекта"}, "element": {"brand-picker"}, "sources": {string(sources)}}
	req := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.brands.Name)+"?"+query.Encode(), nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), styleContextUser()))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("необъявленный источник принят: %s", rec.Body.String())
	}
}

// Форма сохранённого звонка: варианты брендов сразу оформлены по направлению
// звонка, без запроса из браузера.
func TestChoiceRowStyle_BrandInitialRenderUsesSavedDirection(t *testing.T) {
	f := newStyleContextFixture(t)
	opts := f.renderedOptions(t, "Бренд")
	if !strings.Contains(strings.ToLower(opts[f.red.String()]), "background:#ffc0c0") {
		t.Errorf("форма: бренд без фона из карточки: %q", opts[f.red.String()])
	}
	if strings.Contains(opts[f.noColor.String()], "style=") {
		t.Errorf("форма: бренд без цвета оформлен: %q", opts[f.noColor.String()])
	}
}

// Архивная реклама: в группе «Архивные» и в её подгруппах — оранжево-красная и
// недоступна для выбора; остальная реклама — как обычно.
func TestChoiceRowStyle_ArchivedAdvertising(t *testing.T) {
	f := newStyleContextFixture(t)
	got := f.options(t, f.ads.Name, url.Values{})
	for name, id := range map[string]uuid.UUID{"в группе Архивные": f.archived, "в подгруппе Архивные/2025": f.nested} {
		o, ok := got[id]
		if !ok {
			t.Fatalf("%s: строки нет в выдаче — должна быть видна: %v", name, got)
		}
		if !strings.Contains(strings.ToLower(o.style), "color:#ff4500") || !o.disabled {
			t.Errorf("%s: ждали color:#FF4500 и запрет выбора, получено %+v", name, o)
		}
	}
	if o := got[f.current]; o.style != "" || o.disabled {
		t.Errorf("действующая реклама оформлена: %+v", o)
	}

	opts := f.renderedOptions(t, "Реклама")
	if o := opts[f.nested.String()]; !strings.Contains(" "+o, " disabled=") || !strings.Contains(strings.ToLower(o), "#ff4500") {
		t.Errorf("форма: архивная реклама без disabled/цвета: %q", o)
	}
	if o := opts[f.current.String()]; strings.Contains(o, "disabled") {
		t.Errorf("форма: действующая реклама недоступна: %q", o)
	}
}

// Сохранённая архивная реклама остаётся в поле: её <option> выбран и НЕ
// disabled — значение disabled-варианта браузер не отправляет, и запись формы
// молча стёрла бы ссылку. Остальные архивные строки по-прежнему недоступны.
func TestChoiceRowStyle_ArchivedSavedValueSurvives(t *testing.T) {
	f := newStyleContextFixture(t)
	opts := f.renderedOptionsOf(t, f.archivedCallID, "Реклама")
	o := opts[f.nested.String()]
	if !strings.Contains(o, `selected=`) || strings.Contains(" "+o, ` disabled=`) {
		t.Errorf("сохранённая архивная реклама: ждали selected без disabled, получено %q", o)
	}
	if !strings.Contains(o, "data-ob-ref-disabled") || !strings.Contains(strings.ToLower(o), "#ff4500") {
		t.Errorf("сохранённая архивная реклама потеряла оформление: %q", o)
	}
	if a := opts[f.archived.String()]; !strings.Contains(" "+a, ` disabled=`) {
		t.Errorf("другая архивная строка выбирается: %q", a)
	}
}

// renderedOptions — атрибуты <option> поля выбора field в форме
// сохранённого звонка, по значению.
func (f styleContextFixture) renderedOptions(t *testing.T, field string) map[string]string {
	t.Helper()
	return f.renderedOptionsOf(t, f.callID, field)
}

func (f styleContextFixture) renderedOptionsOf(t *testing.T, callID uuid.UUID, field string) map[string]string {
	t.Helper()
	router := chi.NewRouter()
	f.server.Mount(router)
	req := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(f.call.Name)+"/"+callID.String(), nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), styleContextUser()))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("form status=%d body=%s", rec.Code, rec.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	sel := findSelectByName(doc, field)
	if sel == nil {
		t.Fatalf("select %q not found", field)
	}
	out := map[string]string{}
	for opt := sel.FirstChild; opt != nil; opt = opt.NextSibling {
		if opt.Type != html.ElementNode || opt.Data != "option" {
			continue
		}
		value, _ := htmlAttribute(opt, "value")
		var b strings.Builder
		for _, a := range opt.Attr {
			b.WriteString(a.Key + `="` + a.Val + `" `)
		}
		out[value] = b.String()
	}
	return out
}
