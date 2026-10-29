package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Оформление строк выбора направлений, как в форме выбора 1С: ограниченный
// приём — красный текст, частично ограниченный — жёлтый фон, и только для
// сотрудника колл-центра (подразделение из карточки, роль или функция
// конфигурации). Остальным и обычным направлениям — без оформления.

type rowStyleFixture struct {
	s                     *Server
	db                    *storage.DB
	dirs, doc             *metadata.Entity
	full, part, plain     uuid.UUID
	operator, storekeeper *auth.User
}

func newRowStyleFixture(t *testing.T, style *metadata.ChoiceRowStyle, module string) *rowStyleFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	deps := &metadata.Entity{Name: "Подразделения", Kind: metadata.KindCatalog,
		Fields:     []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		Predefined: []*metadata.PredefinedItem{{Name: "КоллЦентр", Fields: map[string]any{"Наименование": "Колл центр"}}, {Name: "Склад", Fields: map[string]any{"Наименование": "Склад"}}}}
	card := &metadata.Entity{Name: "Пользователи", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "УчётнаяЗапись", Type: metadata.FieldType("reference:_users"), RefEntity: "_users"},
		{Name: "Подразделение", Type: metadata.FieldType("reference:Подразделения"), RefEntity: "Подразделения"},
	}}
	dirs := &metadata.Entity{Name: "Направления", Kind: metadata.KindCatalog, ChoiceRowStyle: style, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "ОграниченПриемЗаявокДляКЦ", Type: metadata.FieldTypeBool},
		{Name: "ЧастичноОграниченПриемЗаявокДляКЦ", Type: metadata.FieldTypeBool},
	}}
	doc := &metadata.Entity{Name: "Звонок", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Направление", Type: metadata.FieldType("reference:Направления"), RefEntity: "Направления"},
	}}
	ents := []*metadata.Entity{deps, card, dirs, doc}
	f := &rowStyleFixture{db: db, dirs: dirs, doc: doc, full: uuid.New(), part: uuid.New(), plain: uuid.New()}
	f.s = newSubmitTestServerOn(t, db, ents)
	if err := db.SyncAllPredefined(ctx, ents); err != nil {
		t.Fatal(err)
	}
	if module != "" {
		prog, err := parser.New(lexer.New(module, "кц.module.os")).ParseProgram()
		if err != nil {
			t.Fatal(err)
		}
		f.s.reg.LoadModules(map[string]*ast.Program{"КЦ": prog})
		f.s.interp.LookupModuleProc = f.s.reg.GetModuleNamespacedProc
	}
	for _, r := range []struct {
		id         uuid.UUID
		name       string
		full, part bool
	}{{f.full, "Холодильники", true, false}, {f.part, "Стиральные", false, true}, {f.plain, "Плиты", false, false}} {
		if err := db.Upsert(ctx, dirs.Name, r.id, map[string]any{"Наименование": r.name, "ОграниченПриемЗаявокДляКЦ": r.full, "ЧастичноОграниченПриемЗаявокДляКЦ": r.part}, dirs); err != nil {
			t.Fatal(err)
		}
	}
	kc, err := db.GetPredefinedIDStr(ctx, "Подразделения", "КоллЦентр")
	if err != nil {
		t.Fatal(err)
	}
	wh, _ := db.GetPredefinedIDStr(ctx, "Подразделения", "Склад")
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	account := func(login, role string) *auth.User {
		t.Helper()
		u, err := repo.Create(ctx, login, "password1", login, false)
		if err != nil {
			t.Fatal(err)
		}
		u.Roles = []*auth.Role{{Name: role, Permissions: auth.Permission{
			Catalogs:  map[string][]string{"Направления": {"read"}},
			Documents: map[string][]string{"Звонок": {"read", "write", "create"}},
		}}}
		return u
	}
	f.operator = account("Оператор_075", "ОператорКЦ")
	f.storekeeper = account("Кладовщик", "Кладовщик")
	for _, c := range []struct {
		u   *auth.User
		dep string
	}{{f.operator, kc}, {f.storekeeper, wh}} {
		if err := db.Upsert(ctx, card.Name, uuid.New(), map[string]any{"Наименование": c.u.Login, "УчётнаяЗапись": c.u.ID, "Подразделение": c.dep}, card); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func directionsStyle() []metadata.ChoiceStyleRule {
	return []metadata.ChoiceStyleRule{
		{When: "ОграниченПриемЗаявокДляКЦ", Style: metadata.FormCellStyle{Color: "rgb(250,0,0)"}},
		{When: "ЧастичноОграниченПриемЗаявокДляКЦ", Style: metadata.FormCellStyle{Background: "rgb(255,255,0)"}},
	}
}

// styles — стиль каждой строки выдачи /ui/_ref-options для пользователя.
func (f *rowStyleFixture) styles(t *testing.T, u *auth.User) map[uuid.UUID]string {
	t.Helper()
	rec := serveRefOptions(t, f.s, f.dirs.Name, "", u)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	out := map[uuid.UUID]string{}
	for _, it := range got.Items {
		id := uuid.MustParse(it["id"].(string))
		st, _ := it["_style"].(string)
		out[id] = st
	}
	return out
}

func (f *rowStyleFixture) assertStyled(t *testing.T, who string, st map[uuid.UUID]string, styled bool) {
	t.Helper()
	if styled {
		if !strings.Contains(st[f.full], "color:") || !strings.Contains(st[f.part], "background:") || st[f.plain] != "" {
			t.Errorf("%s: ждали красный текст у ограниченного, жёлтый фон у частичного, обычное без стиля: %v", who, st)
		}
		return
	}
	for id, s := range st {
		if s != "" {
			t.Errorf("%s: оформления быть не должно, у %s стиль %q", who, id, s)
		}
	}
}

func TestChoiceRowStyle_ForUserByCard(t *testing.T) {
	f := newRowStyleFixture(t, &metadata.ChoiceRowStyle{
		ForUser: &metadata.ChoiceStyleUser{Card: "Пользователи", Field: "Подразделение", Equals: "Подразделения.КоллЦентр"},
		Rules:   directionsStyle(),
	}, "")
	f.assertStyled(t, "оператор колл-центра", f.styles(t, f.operator), true)
	f.assertStyled(t, "кладовщик", f.styles(t, f.storekeeper), false)
	f.assertStyled(t, "без карточки", f.styles(t, &auth.User{ID: uuid.NewString(), Login: "admin", IsAdmin: true}), false)
	f.assertStyled(t, "аноним", f.styles(t, nil), false)
}

func TestChoiceRowStyle_ForRoles(t *testing.T) {
	f := newRowStyleFixture(t, &metadata.ChoiceRowStyle{ForRoles: []string{"ОператорКЦ"}, Rules: directionsStyle()}, "")
	f.assertStyled(t, "роль ОператорКЦ", f.styles(t, f.operator), true)
	f.assertStyled(t, "другая роль", f.styles(t, f.storekeeper), false)
}

func TestChoiceRowStyle_ForUserFunc(t *testing.T) {
	module := `Функция ПользовательКоллЦентра() Экспорт
	Возврат ИмяПользователя() = "Оператор_075";
КонецФункции

Функция ПоИмени() Экспорт
	Возврат ТекущийПользователь().Имя = "Оператор_075";
КонецФункции

Функция ПоИД() Экспорт
	Возврат СтрДлина(Строка(ТекущийПользователь().ИД)) = 36;
КонецФункции

Функция Сломана() Экспорт
	ВызватьИсключение "нет данных";
КонецФункции
`
	f := newRowStyleFixture(t, &metadata.ChoiceRowStyle{ForUser: &metadata.ChoiceStyleUser{Func: "КЦ.ПользовательКоллЦентра"}, Rules: directionsStyle()}, module)
	f.assertStyled(t, "функция вернула Истина", f.styles(t, f.operator), true)
	f.assertStyled(t, "функция вернула Ложь", f.styles(t, f.storekeeper), false)
	f.dirs.ChoiceRowStyle.ForUser.Func = "КЦ.ПоИмени"
	f.assertStyled(t, "ТекущийПользователь().Имя — оператор", f.styles(t, f.operator), true)
	f.assertStyled(t, "ТекущийПользователь().Имя — кладовщик", f.styles(t, f.storekeeper), false)
	f.dirs.ChoiceRowStyle.ForUser.Func = "КЦ.ПоИД"
	f.assertStyled(t, "ТекущийПользователь().ИД", f.styles(t, f.operator), true)
	f.dirs.ChoiceRowStyle.ForUser.Func = "КЦ.Сломана"
	f.assertStyled(t, "ошибка функции — без оформления", f.styles(t, f.operator), false)
	f.dirs.ChoiceRowStyle.ForUser.Func = "КЦ.НетТакой"
	f.assertStyled(t, "нет функции — без оформления", f.styles(t, f.operator), false)
}

// Выпадающий список поля документа: <option> несёт style оформления.
func TestChoiceRowStyle_DocumentFormOptions(t *testing.T) {
	f := newRowStyleFixture(t, &metadata.ChoiceRowStyle{ForRoles: []string{"ОператорКЦ"}, Rules: directionsStyle()}, "")
	render := func(u *auth.User) string {
		req := reqWithChi(http.MethodGet, "/ui/document/Звонок/new", nil, map[string]string{"kind": "document", "entity": f.doc.Name})
		req = req.WithContext(auth.ContextWithUser(req.Context(), u))
		rec := httptest.NewRecorder()
		f.s.form(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("форма: %d %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	body := render(f.operator)
	optionOf := func(body string, id uuid.UUID) string {
		i := strings.Index(body, `<option value="`+id.String()+`"`)
		if i < 0 {
			t.Fatalf("нет варианта %s", id)
		}
		j := strings.Index(body[i:], ">")
		return body[i : i+j]
	}
	if o := optionOf(body, f.full); !strings.Contains(o, `style="color:`) {
		t.Errorf("ограниченное направление без красного текста: %s", o)
	}
	if o := optionOf(body, f.part); !strings.Contains(o, `style="background:`) {
		t.Errorf("частично ограниченное без жёлтого фона: %s", o)
	}
	if o := optionOf(body, f.plain); strings.Contains(o, "style=") {
		t.Errorf("обычное направление оформлено: %s", o)
	}
	if o := optionOf(render(f.storekeeper), f.full); strings.Contains(o, "style=") {
		t.Errorf("не оператору оформление не положено: %s", o)
	}
}
