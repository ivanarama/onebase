package entityservice

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Значения по умолчанию у реквизитов (план 153).

func defaultsMeta() (*metadata.Entity, *metadata.Entity, []*metadata.Constant, []*metadata.Enum) {
	org := &metadata.Entity{
		Name: "Организация",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	enum := &metadata.Enum{Name: "СпособУчета", Values: []string{"ВТомЧисле", "Сверху"}}
	doc := &metadata.Entity{
		Name: "Реализация",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate, Default: "сейчас"},
			{Name: "ДеньОтгрузки", Type: metadata.FieldTypeDate, Default: "сегодня"},
			{Name: "Организация", Type: "reference:Организация", RefEntity: "Организация", Default: "Константа.НашаОрганизация"},
			{Name: "Ответственный", Type: metadata.FieldTypeString, Default: "ТекущийПользователь"},
			{Name: "Комментарий", Type: metadata.FieldTypeString, Default: "Без комментария"},
			{Name: "Скидка", Type: metadata.FieldTypeNumber, Default: "12,5"},
			{Name: "Проведён", Type: metadata.FieldTypeBool, Default: "Истина"},
			{Name: "СпособУчетаНДС", Type: "enum:СпособУчета", EnumName: "СпособУчета", Default: "ВТомЧисле"},
			{Name: "БезДефолта", Type: metadata.FieldTypeString},
		},
	}
	constants := []*metadata.Constant{
		{Name: "НашаОрганизация", Type: "reference:Организация", RefEntity: "Организация"},
	}
	return doc, org, constants, []*metadata.Enum{enum}
}

func defaultsFixture(t *testing.T) (context.Context, *storage.DB, *Service, *metadata.Entity, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "defaults.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc, org, constants, enums := defaultsMeta()
	if err := db.Migrate(ctx, []*metadata.Entity{doc, org}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateConstants(ctx, constants); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc, org},
		Enums:     enums,
		Constants: constants,
	})
	return ctx, db, &Service{Store: db, Reg: reg, Interp: interpreter.New()}, doc, org
}

// Конфигурация обязана проходить ту же валидацию, что и на onebase check:
// иначе фикстура теста может быть невалидной, а тест — зелёным.
func TestDefaults_ФикстураПроходитВалидацию(t *testing.T) {
	doc, org, constants, enums := defaultsMeta()
	if err := metadata.ValidateDefaults([]*metadata.Entity{doc, org}, enums, constants); err != nil {
		t.Fatalf("метаданные теста не прошли ValidateDefaults: %v", err)
	}
}

func TestApplyDefaults_ИсточникиЗначений(t *testing.T) {
	ctx, db, svc, doc, org := defaultsFixture(t)

	orgID := uuid.New()
	if err := db.Upsert(ctx, org.Name, orgID, map[string]any{"Наименование": "ООО Ромашка"}, org); err != nil {
		t.Fatal(err)
	}
	if err := db.SetConstant(ctx, "НашаОрганизация", orgID.String()); err != nil {
		t.Fatal(err)
	}
	ctx = auth.ContextWithUser(ctx, &auth.User{ID: "u1", Login: "ivan"})

	fields := map[string]any{}
	filled, err := svc.ApplyDefaults(ctx, doc, fields, DefaultsOptions{})
	if err != nil {
		t.Fatalf("ApplyDefaults: %v", err)
	}
	if len(filled) == 0 {
		t.Fatal("ни одно поле не заполнено")
	}

	if got := fields["Организация"]; got != orgID.String() {
		t.Errorf("Организация = %v, ожидалось %s (значение константы)", got, orgID)
	}
	if got := fields["Ответственный"]; got != "ivan" {
		t.Errorf("Ответственный = %v, ожидался логин текущего пользователя", got)
	}
	if got := fields["Комментарий"]; got != "Без комментария" {
		t.Errorf("Комментарий = %v", got)
	}
	if got := fields["Скидка"]; got != 12.5 {
		t.Errorf("Скидка = %v (%T), ожидалось 12.5 числом", got, got)
	}
	if got := fields["Проведён"]; got != true {
		t.Errorf("Проведён = %v (%T), ожидалось true булевым", got, got)
	}
	if got := fields["СпособУчетаНДС"]; got != "ВТомЧисле" {
		t.Errorf("СпособУчетаНДС = %v", got)
	}
	now, ok := fields["Дата"].(time.Time)
	if !ok {
		t.Fatalf("Дата = %T, ожидалось time.Time", fields["Дата"])
	}
	if time.Since(now) > time.Minute {
		t.Errorf("Дата = %v, ожидалось «сейчас»", now)
	}
	day, ok := fields["ДеньОтгрузки"].(time.Time)
	if !ok {
		t.Fatalf("ДеньОтгрузки = %T, ожидалось time.Time", fields["ДеньОтгрузки"])
	}
	if day.Hour() != 0 || day.Minute() != 0 || day.Second() != 0 {
		t.Errorf("ДеньОтгрузки = %v, ожидалось начало дня", day)
	}
	if _, exists := fields["БезДефолта"]; exists {
		t.Errorf("реквизит без ключа default заполнен: %v", fields["БезДефолта"])
	}
}

// Дефолт — то, с чего человек начинает, а не то, чем платформа исправляет
// введённое. Значение, пришедшее от пользователя, обязано пережить применение.
func TestApplyDefaults_НеПеретираетЗаполненное(t *testing.T) {
	ctx, _, svc, doc, _ := defaultsFixture(t)
	ctx = auth.ContextWithUser(ctx, &auth.User{ID: "u1", Login: "ivan"})

	chosen := time.Date(2020, 3, 4, 5, 6, 0, 0, time.UTC)
	fields := map[string]any{
		"Комментарий":   "Ручной ввод",
		"Дата":          chosen,
		"Скидка":        float64(3),
		"Ответственный": "petr",
	}
	if _, err := svc.ApplyDefaults(ctx, doc, fields, DefaultsOptions{}); err != nil {
		t.Fatal(err)
	}
	if fields["Комментарий"] != "Ручной ввод" || fields["Скидка"] != float64(3) || fields["Ответственный"] != "petr" {
		t.Errorf("дефолт перетёр введённое: %v", fields)
	}
	if got := fields["Дата"].(time.Time); !got.Equal(chosen) {
		t.Errorf("Дата = %v, ожидалось %v", got, chosen)
	}
}

// Ключи полей приходят и в PascalCase (формы), и в нижнем регистре
// (Object.Set). Дефолт обязан видеть заполненность в обоих написаниях, иначе
// рядом появится второй ключ того же поля и в базу уедет он.
func TestApplyDefaults_РегистрКлючейНеСоздаётДубль(t *testing.T) {
	ctx, _, svc, doc, _ := defaultsFixture(t)
	fields := map[string]any{"комментарий": "своё"}
	if _, err := svc.ApplyDefaults(ctx, doc, fields, DefaultsOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, dup := fields["Комментарий"]; dup {
		t.Errorf("появился второй ключ того же поля: %v", fields)
	}
	if fields["комментарий"] != "своё" {
		t.Errorf("значение перетёрто: %v", fields["комментарий"])
	}
}

// Пустая константа — не ошибка: поле просто остаётся пустым. Иначе новая база,
// где константы ещё не заполнены, не давала бы создать ни одного документа.
func TestApplyDefaults_ПустаяКонстантаОставляетПолеПустым(t *testing.T) {
	ctx, _, svc, doc, _ := defaultsFixture(t)
	fields := map[string]any{}
	if _, err := svc.ApplyDefaults(ctx, doc, fields, DefaultsOptions{}); err != nil {
		t.Fatalf("пустая константа не должна быть ошибкой: %v", err)
	}
	if v, ok := fields["Организация"]; ok {
		t.Errorf("Организация заполнена значением %v при пустой константе", v)
	}
}

// Умолчания формы ввода (дата документа, признак активности) существовали до
// плана 153 и остаются только у формы: включать их в DSL и REST значило бы
// изменить поведение уже написанных конфигураций.
func TestApplyDefaults_УмолчанияФормыТолькоПоФлагу(t *testing.T) {
	ctx, db, svc, _, _ := defaultsFixture(t)
	doc := &metadata.Entity{
		Name: "Акт",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Активный", Type: metadata.FieldTypeBool},
		},
		Activity: &metadata.ActivityConfig{Field: "Активный", DefaultScope: metadata.ActivityScopeActive},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}

	programmatic := map[string]any{}
	if _, err := svc.ApplyDefaults(ctx, doc, programmatic, DefaultsOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(programmatic) != 0 {
		t.Errorf("программный путь получил умолчания формы: %v", programmatic)
	}

	form := map[string]any{}
	if _, err := svc.ApplyDefaults(ctx, doc, form, DefaultsOptions{FormEntry: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := form["Дата"].(time.Time); !ok {
		t.Errorf("форма не получила дату документа: %v", form)
	}
	if form["Активный"] != true {
		t.Errorf("форма не получила признак активности: %v", form)
	}
}

// fieldValueFold читает поле независимо от регистра ключа: дефолт кладёт
// написание из метаданных, DSL-присваивание — своё.
func fieldValueFold(fields map[string]any, name string) any {
	for k, v := range fields {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return nil
}

// Хук ПриСозданииНового главнее метаданных: он видит контекст, а YAML — нет.
func TestNewObject_ХукПеребиваетДекларативныйДефолт(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "hook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name: "Заявка",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Комментарий", Type: metadata.FieldTypeString, Default: "из метаданных"},
			{Name: "Источник", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	src := `Процедура ПриСозданииНового(Объект)
  Объект.Комментарий = "из хука";
  Объект.Источник = "хук";
КонецПроцедуры`
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{doc.Name: mustParseProc(t, src)},
	})
	svc := &Service{Store: db, Reg: reg, Interp: interpreter.New()}

	res, err := svc.NewObject(ctx, NewObjectRequest{Entity: doc})
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	if res.DSLError != "" {
		t.Fatalf("хук вернул ошибку: %s", res.DSLError)
	}
	if got := fieldValueFold(res.Object.Fields, "Комментарий"); got != "из хука" {
		t.Errorf("Комментарий = %v, ожидалось значение из хука", got)
	}
	if got := fieldValueFold(res.Object.Fields, "Источник"); got != "хук" {
		t.Errorf("Источник = %v", got)
	}
}

// Ошибка в хуке возвращается как DSLError, а не как технический сбой: форма
// должна открыться и показать причину, а не отдать 500.
func TestNewObject_ОшибкаХукаНеРонаетСоздание(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "hookerr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name:   "Заявка",
		Kind:   metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Комментарий", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	src := `Процедура ПриСозданииНового(Объект)
  ВызватьИсключение("нельзя создавать");
КонецПроцедуры`
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{doc.Name: mustParseProc(t, src)},
	})
	svc := &Service{Store: db, Reg: reg, Interp: interpreter.New()}

	res, err := svc.NewObject(ctx, NewObjectRequest{Entity: doc})
	if err != nil {
		t.Fatalf("ожидался DSLError, получен технический сбой: %v", err)
	}
	if !strings.Contains(res.DSLError, "нельзя создавать") {
		t.Errorf("DSLError = %q, ожидалось сообщение хука", res.DSLError)
	}
	if res.Object == nil {
		t.Error("объект не возвращён — форме нечего показать")
	}
}

// Значения, переданные вызывающим (тело REST-запроса), главнее дефолтов и
// видны хуку: для REST «ввод пользователя» приходит целиком сразу.
func TestNewObject_ПереданныеЗначенияГлавнееДефолта(t *testing.T) {
	ctx, _, svc, doc, _ := defaultsFixture(t)
	res, err := svc.NewObject(ctx, NewObjectRequest{
		Entity: doc,
		Fields: map[string]any{"Комментарий": "из запроса"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Object.Fields["Комментарий"]; got != "из запроса" {
		t.Errorf("Комментарий = %v, ожидалось значение из запроса", got)
	}
}

// Матричный тест: источник `единственный` ходит в БД, поэтому проверяется на
// обоих диалектах — раздельные тесты расхождения не показали бы (CLAUDE.md).
func TestApplyDefaults_ЕдинственныйМатрица(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		склад := &metadata.Entity{
			Name:         "Склад",
			Kind:         metadata.KindCatalog,
			Hierarchical: true,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Активный", Type: metadata.FieldTypeBool},
			},
			Activity: &metadata.ActivityConfig{Field: "Активный", DefaultScope: metadata.ActivityScopeActive},
		}
		doc := &metadata.Entity{
			Name: "Перемещение",
			Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Склад", Type: "reference:Склад", RefEntity: "Склад", Default: "единственный"},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{склад, doc}); err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{склад, doc}})
		svc := &Service{Store: db, Reg: reg, Interp: interpreter.New()}

		applied := func(t *testing.T) (string, bool) {
			t.Helper()
			fields := map[string]any{}
			if _, err := svc.ApplyDefaults(ctx, doc, fields, DefaultsOptions{}); err != nil {
				t.Fatalf("ApplyDefaults: %v", err)
			}
			v, ok := fields["Склад"]
			if !ok {
				return "", false
			}
			return v.(string), true
		}

		addWarehouse := func(t *testing.T, name string, active bool, folder bool, marked bool) uuid.UUID {
			t.Helper()
			id := uuid.New()
			fields := map[string]any{"Наименование": name, "Активный": active}
			if folder {
				fields["is_folder"] = true
			}
			if err := db.Upsert(ctx, склад.Name, id, fields, склад); err != nil {
				t.Fatal(err)
			}
			if marked {
				if err := db.MarkForDeletion(ctx, склад.Name, id, true); err != nil {
					t.Fatal(err)
				}
			}
			return id
		}

		t.Run("ноль элементов — поле пустое", func(t *testing.T) {
			if v, ok := applied(t); ok {
				t.Errorf("подставлено %q при пустом справочнике", v)
			}
		})

		main := addWarehouse(t, "Основной", true, false, false)
		t.Run("ровно один — подставлен", func(t *testing.T) {
			v, ok := applied(t)
			if !ok {
				t.Fatal("единственный элемент не подставлен")
			}
			if v != main.String() {
				t.Errorf("подставлен %q, ожидался %s", v, main)
			}
		})

		addWarehouse(t, "Группа", true, true, false)
		t.Run("группа не считается элементом", func(t *testing.T) {
			v, ok := applied(t)
			if !ok || v != main.String() {
				t.Errorf("группа сбила подстановку: v=%q ok=%v", v, ok)
			}
		})

		addWarehouse(t, "Удалённый", true, false, true)
		t.Run("помеченный на удаление не считается", func(t *testing.T) {
			v, ok := applied(t)
			if !ok || v != main.String() {
				t.Errorf("помеченный сбил подстановку: v=%q ok=%v", v, ok)
			}
		})

		addWarehouse(t, "Закрытый", false, false, false)
		t.Run("неактивный не считается", func(t *testing.T) {
			v, ok := applied(t)
			if !ok || v != main.String() {
				t.Errorf("неактивный сбил подстановку: v=%q ok=%v", v, ok)
			}
		})

		addWarehouse(t, "Второй", true, false, false)
		t.Run("два элемента — поле пустое", func(t *testing.T) {
			if v, ok := applied(t); ok {
				t.Errorf("подставлено %q при двух доступных элементах", v)
			}
		})
	})
}
