package ui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// ЗначениеРеквизитаОбъекта читает реквизит по ссылке: скалярный — как есть,
// ссылочный — как ссылку (для цепочек). Неизвестный реквизит → ошибка.
func TestObjectAttributeValue(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	контрагент := &metadata.Entity{
		Name: "Контрагент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ИНН", Type: metadata.FieldTypeString},
		},
	}
	номенклатура := &metadata.Entity{
		Name: "Номенклатура", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Артикул", Type: metadata.FieldTypeString},
			{Name: "СтавкаНДС", Type: metadata.FieldTypeNumber},
			{Name: "Поставщик", Type: "reference:Контрагент", RefEntity: "Контрагент"},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{контрагент, номенклатура}); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{контрагент, номенклатура}})
	s := &Server{store: db, reg: registry}

	catalogs := interpreter.NewCatalogsRoot(interpreter.NewStaticCtx(ctx), db, registry)
	create := func(entityName string, set map[string]any) *interpreter.Ref {
		t.Helper()
		cp := catalogs.Get(entityName).(*interpreter.CatalogProxy)
		w := cp.CallMethod("создать", nil).(*interpreter.CatalogRecordWriter)
		for k, v := range set {
			w.Set(k, v)
		}
		return w.CallMethod("записать", nil).(*interpreter.Ref)
	}

	постРеф := create("Контрагент", map[string]any{"Наименование": "ООО Поставщик", "ИНН": "7701234567"})
	номРеф := create("Номенклатура", map[string]any{
		"Наименование": "Стул",
		"Артикул":      "A-1",
		"СтавкаНДС":    float64(20),
		"Поставщик":    постРеф,
	})

	// Скалярный реквизит (число).
	v, err := s.objectAttributeValue(ctx, []any{номРеф, "СтавкаНДС"})
	if err != nil {
		t.Fatalf("СтавкаНДС: %v", err)
	}
	if toFloat(v) != 20 {
		t.Errorf("СтавкаНДС = %v (%T), ожидалось 20", v, v)
	}

	// Ссылочный реквизит → *Ref с наименованием и типом.
	pv, err := s.objectAttributeValue(ctx, []any{номРеф, "Поставщик"})
	if err != nil {
		t.Fatalf("Поставщик: %v", err)
	}
	pref, ok := pv.(*interpreter.Ref)
	if !ok {
		t.Fatalf("Поставщик → %T, ожидался *interpreter.Ref", pv)
	}
	if pref.Name != "ООО Поставщик" || pref.Type != "Контрагент" {
		t.Errorf("ссылка-реквизит: name=%q type=%q", pref.Name, pref.Type)
	}

	// Цепочка: реквизит ссылки, полученной из реквизита.
	chained, err := s.objectAttributeValue(ctx, []any{pref, "Наименование"})
	if err != nil {
		t.Fatalf("цепочка: %v", err)
	}
	if chained != "ООО Поставщик" {
		t.Errorf("цепочка дала %q, ожидалось «ООО Поставщик»", chained)
	}

	bulk, err := s.objectAttributeValues(ctx, []any{
		interpreter.NewArray([]any{номРеф}),
		"Номенклатура",
		interpreter.NewArray([]any{"Наименование", "Поставщик"}),
	})
	if err != nil {
		t.Fatalf("ЗначенияРеквизитовОбъектов: %v", err)
	}
	bulkMap, ok := bulk.(*interpreter.Map)
	if !ok {
		t.Fatalf("bulk → %T, ожидался *interpreter.Map", bulk)
	}
	bulkRow, ok := bulkMap.CallMethod("получить", []any{номРеф}).(*interpreter.Struct)
	if !ok {
		t.Fatalf("bulk row → %T, ожидалась *interpreter.Struct", bulkMap.CallMethod("получить", []any{номРеф}))
	}
	if got := bulkRow.Get("Наименование"); got != "Стул" {
		t.Errorf("bulk Наименование = %v, ожидалось Стул", got)
	}
	bulkProvider, ok := bulkRow.Get("Поставщик").(*interpreter.Ref)
	if !ok {
		t.Fatalf("bulk Поставщик → %T, ожидался *interpreter.Ref", bulkRow.Get("Поставщик"))
	}
	if bulkProvider.Name != "ООО Поставщик" || bulkProvider.Type != "Контрагент" {
		t.Errorf("bulk ссылка-реквизит: name=%q type=%q", bulkProvider.Name, bulkProvider.Type)
	}

	src := `Функция Тест()
  Ссылки = [СсылкаНом];
  Реквизиты = ЗначенияРеквизитовОбъектов(Ссылки, "Номенклатура", ["Наименование", "Поставщик"]);
  Возврат Реквизиты[СсылкаНом].Поставщик.Наименование;
КонецФункции`
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	if err != nil {
		t.Fatalf("parse DSL bulk sample: %v", err)
	}
	interp := interpreter.New()
	vars := s.buildDSLVars(ctx, runtime.NewMovementsCollector("test", uuid.Nil))
	vars["СсылкаНом"] = номРеф
	var result any
	if err := interp.RunWithResult(prog.Procedures[0], nil, &result, vars); err != nil {
		t.Fatalf("run DSL bulk sample: %v", err)
	}
	if result != "ООО Поставщик" {
		t.Errorf("DSL sample result = %v, ожидалось ООО Поставщик", result)
	}

	doc := &metadata.Entity{
		Name: "ЗаказПокупателя", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "ОсновнаяНоменклатура", Type: "reference:Номенклатура", RefEntity: "Номенклатура"},
		},
		TableParts: []metadata.TablePart{{
			Name: "Товары",
			Fields: []metadata.Field{
				{Name: "Номенклатура", Type: "reference:Номенклатура", RefEntity: "Номенклатура"},
			},
		}},
	}
	obj := runtime.NewObject(doc.Name, doc.Kind)
	obj.Fields["ОсновнаяНоменклатура"] = номРеф
	obj.TablePartRows["Товары"] = []map[string]any{{"Номенклатура": номРеф}}
	thisObj := s.newFormObjectThis(ctx, obj, doc, nil)
	src = `Функция Тест()
  Рез = Объект.ОсновнаяНоменклатура.Артикул;
  Для Каждого Стр Из Объект.Товары Цикл
    Рез = Рез + "|" + Стр.Номенклатура.Артикул;
    Если Стр.Номенклатура.Поставщик.ИНН <> Неопределено Тогда
      Рез = "double-deref";
    КонецЕсли;
  КонецЦикла;
  Возврат Рез;
КонецФункции`
	l = lexer.New(src, "test.os")
	p = parser.New(l)
	prog, err = p.ParseProgram()
	if err != nil {
		t.Fatalf("parse DSL ref attr sample: %v", err)
	}
	result = nil
	if err := interp.RunWithResult(prog.Procedures[0], thisObj, &result, map[string]any{"Объект": thisObj}); err != nil {
		t.Fatalf("run DSL ref attr sample: %v", err)
	}
	if result != "A-1|A-1" {
		t.Errorf("DSL ref attr sample result = %v, ожидалось A-1|A-1", result)
	}

	// Неизвестный реквизит → ошибка, а не тихий nil.
	if _, err := s.objectAttributeValue(ctx, []any{номРеф, "НетТакого"}); err == nil {
		t.Error("ожидалась ошибка для несуществующего реквизита")
	}

	// Пустой/nil первый аргумент → nil без ошибки.
	if v, err := s.objectAttributeValue(ctx, []any{nil, "СтавкаНДС"}); v != nil || err != nil {
		t.Errorf("nil-ссылка: v=%v err=%v, ожидалось nil/nil", v, err)
	}
	empty := &interpreter.Ref{Type: "Номенклатура"}
	if v, err := s.objectAttributeValue(ctx, []any{empty, "СтавкаНДС"}); v != nil || err != nil {
		t.Errorf("пустая ссылка: v=%v err=%v, ожидалось nil/nil", v, err)
	}
}
