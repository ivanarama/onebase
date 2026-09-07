package ui

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

func TestFormObjectThis_DeclaredEmptyValuesAreTypedWithoutMutation(t *testing.T) {
	target := &metadata.Entity{Name: "Контрагенты", Kind: metadata.KindCatalog}
	entity := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
			{Name: "Флаг", Type: metadata.FieldTypeBool},
			{Name: "Текст", Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Статус", Type: "enum:Статусы", EnumName: "Статусы"},
			{Name: "Контрагент", Type: "reference:Контрагенты", RefEntity: "Контрагенты"},
			{Name: "Картинка", Type: metadata.FieldTypeImage},
		},
	}
	form := &metadata.FormModule{Attributes: []*metadata.FormAttribute{
		{Name: "Комментарий", TypeRef: "string(40)", Length: 40},
		{Name: "ДатаФормы", TypeRef: "date"},
		{Name: "Выбранный", TypeRef: "CatalogRef.Контрагенты"},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{target, entity})
	obj := runtime.NewObject(entity.Name, entity.Kind)
	this := s.newFormObjectThis(ctx, obj, entity, form)

	if got, ok := this.Get("Сумма").(decimal.Decimal); !ok || !got.IsZero() {
		t.Fatalf("Сумма = %T(%v), ожидался decimal.Zero", this.Get("Сумма"), this.Get("Сумма"))
	}
	if got := this.Get("Флаг"); got != false {
		t.Fatalf("Флаг = %T(%v)", got, got)
	}
	if got := this.Get("Текст"); got != "" {
		t.Fatalf("Текст = %T(%v)", got, got)
	}
	if got := this.Get("Статус"); got != "" {
		t.Fatalf("Статус = %T(%v)", got, got)
	}
	if got, ok := this.Get("Дата").(time.Time); !ok || !got.IsZero() {
		t.Fatalf("Дата = %T(%v)", this.Get("Дата"), this.Get("Дата"))
	}
	ref, ok := this.Get("Контрагент").(*interpreter.Ref)
	if !ok || ref.UUID != "" || ref.Type != target.Name || ref.Kind != metadata.KindCatalog {
		t.Fatalf("Контрагент = %#v (%T), ожидалась типизированная пустая ссылка", ref, this.Get("Контрагент"))
	}
	if got := this.Get("Картинка"); got != nil {
		t.Fatalf("Картинка = %T(%v), ожидалось Неопределено", got, got)
	}
	if got := this.Get("Комментарий"); got != "" {
		t.Fatalf("Комментарий = %T(%v)", got, got)
	}
	if got, ok := this.Get("ДатаФормы").(time.Time); !ok || !got.IsZero() {
		t.Fatalf("ДатаФормы = %T(%v)", this.Get("ДатаФормы"), this.Get("ДатаФормы"))
	}
	formRef, ok := this.Get("Выбранный").(*interpreter.Ref)
	if !ok || formRef.Type != target.Name || formRef.Kind != metadata.KindCatalog || formRef.UUID != "" {
		t.Fatalf("Выбранный = %#v (%T)", formRef, this.Get("Выбранный"))
	}
	if len(obj.Fields) != 0 {
		t.Fatalf("чтение материализовало пустые значения в объекте: %#v", obj.Fields)
	}
}

func TestFormObjectThis_ValueTableColumnsKeepTypeMetadata(t *testing.T) {
	form := &metadata.FormModule{Attributes: []*metadata.FormAttribute{{
		Name: "Подбор", TypeRef: "ValueTable",
		Columns: []*metadata.FormAttributeColumn{
			{Name: "Количество", TypeRef: "decimal(15,2)"},
			{Name: "Дата", TypeRef: "date"},
		},
	}}}
	obj := runtime.NewObject("Обработка", "")
	this := &formObjectThis{obj: obj, form: form}
	tp := this.Get("Подбор").(*formTpProxy)
	if tp.tp.Fields[0].Type != metadata.FieldTypeNumber || tp.tp.Fields[0].Length != 15 || tp.tp.Fields[0].Scale != 2 {
		t.Fatalf("metadata Количество = %+v", tp.tp.Fields[0])
	}
	row := tp.CallMethod("Добавить", nil).(*interpreter.MapThis)
	if got, ok := row.Get("Количество").(decimal.Decimal); !ok || !got.IsZero() {
		t.Fatalf("Количество = %T(%v)", row.Get("Количество"), row.Get("Количество"))
	}
	if got, ok := row.Get("Дата").(time.Time); !ok || !got.IsZero() {
		t.Fatalf("Дата = %T(%v)", row.Get("Дата"), row.Get("Дата"))
	}
	if len(row.M) != 0 {
		t.Fatalf("чтение создало значения в строке: %#v", row.M)
	}
}

// План 37, этап 8: formObjectThis должен возвращать formTpProxy при Get
// по имени ТЧ, чтобы DSL-выражение Объект.Товары.Добавить() реально
// модифицировало obj.TablePartRows.
func TestFormObjectThis_GetReturnsTpProxy(t *testing.T) {
	entity := &metadata.Entity{
		Name: "Документ",
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{
				{Name: "Количество", Type: metadata.FieldTypeNumber},
			}},
		},
	}
	obj := &runtime.Object{
		ID:            uuid.New(),
		Type:          entity.Name,
		Fields:        map[string]any{},
		TablePartRows: map[string][]map[string]any{},
	}
	this := &formObjectThis{obj: obj, entity: entity}

	got := this.Get("Товары")
	tp, ok := got.(*formTpProxy)
	if !ok {
		t.Fatalf("Get(\"Товары\") = %T, ожидался *formTpProxy", got)
	}
	if tp.tpName != "Товары" {
		t.Errorf("tpName = %q, ожидалось \"Товары\"", tp.tpName)
	}
}

// formObjectThis должен сохранять контракты идентичности и строкового
// представления runtime.Object. Иначе запись `Дв.Документ = this` передаёт в
// драйвер БД внутренний *formObjectThis вместо UUID/display-строки.
func TestFormObjectThis_DelegatesIdentityAndString(t *testing.T) {
	id := uuid.New()
	obj := &runtime.Object{
		ID:     id,
		Type:   "ПриходныйКассовыйОрдер",
		Fields: map[string]any{"номер": "ПКО-00001"},
	}
	this := &formObjectThis{obj: obj}

	if got := this.GetRefUUID(); got != id.String() {
		t.Errorf("GetRefUUID() = %q, ожидался %q", got, id.String())
	}
	if got := this.String(); got != "ПКО-00001" {
		t.Errorf("String() = %q, ожидался номер документа", got)
	}
}

// Оборачивание объекта для формы не должно скрывать его методы. В частности,
// posting-модули используют this.МоментВремени() для корректных срезов остатков.
func TestFormObjectThis_DelegatesObjectMethods(t *testing.T) {
	id := uuid.New()
	period := time.Date(2026, time.July, 22, 12, 30, 0, 0, time.UTC)
	obj := &runtime.Object{
		ID:     id,
		Type:   "РеализацияТоваров",
		Fields: map[string]any{"дата": period},
	}
	this := &formObjectThis{obj: obj}

	got, ok := this.CallMethod("моментвремени", nil).(*runtime.MomentTime)
	if !ok {
		t.Fatalf("МоментВремени() = %T, ожидался *runtime.MomentTime", got)
	}
	if got.DocID != id || got.DocType != obj.Type || !got.Period.Equal(period) {
		t.Errorf("МоментВремени() = %+v, ожидались ID=%s, Type=%s, Period=%s", got, id, obj.Type, period)
	}
}

// Сквозная регрессия: UI проводит документ через formObjectThis, а posting-код
// кладёт сам this одновременно в строковый и ссылочный атрибуты регистра.
// PostgreSQL раньше падал на string-поле с "cannot find encode plan"; SQLite
// также не должен получать внутренний тип UI на границе storage.
func TestFormObjectThis_DirectValueWritesToRegister(t *testing.T) {
	doc := &metadata.Entity{
		Name:    "ПриходныйКассовыйОрдер",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	reg := &metadata.Register{
		Name:       "ДенежныеСредства",
		Dimensions: []metadata.Field{{Name: "Касса", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}},
		Attributes: []metadata.Field{
			{Name: "Документ", Type: metadata.FieldTypeString},
			{Name: "ДокументСсылка", Type: "reference:ПриходныйКассовыйОрдер", RefEntity: "ПриходныйКассовыйОрдер"},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})
	if err := s.store.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}
	posting := mustParse(t, `Процедура ОбработкаПроведения()
	Дв = Движения.ДенежныеСредства.Добавить();
	Дв.Касса = "Основная";
	Дв.Сумма = 100;
	Дв.Документ = this;
	Дв.ДокументСсылка = this;
КонецПроцедуры`)
	s.reg.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Programs:  map[string]*ast.Program{doc.Name: posting},
		Registers: []*metadata.Register{reg},
	})

	id := uuid.New()
	res, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc,
		ID:     id,
		IsNew:  true,
		Fields: map[string]any{"Номер": "ПКО-00001"},
		Action: "post",
	})
	if err != nil {
		t.Fatalf("проведение вернуло техническую ошибку: %v", err)
	}
	if res.DSLError != "" {
		t.Fatalf("проведение вернуло DSL-ошибку: %s", res.DSLError)
	}

	rows, err := s.store.GetMovements(ctx, reg.Name, reg, storage.RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ожидалось одно движение, получено %d", len(rows))
	}
	if got := rows[0]["Документ"]; got != "ПКО-00001" {
		t.Errorf("Документ = %v, ожидалось ПКО-00001", got)
	}
	if got := rows[0]["ДокументСсылка"]; got != id.String() {
		t.Errorf("ДокументСсылка = %v, ожидалось %s", got, id)
	}
}

// Сквозная регрессия для типизированного пустого значения ссылки: чтение
// незаполненного реквизита в posting-модуле возвращает *interpreter.Ref, но
// граница storage обязана записать его как SQL NULL, а не передать драйверу
// внутренний DSL-тип.
func TestFormObjectThis_EmptyReferenceWritesNullToMovement(t *testing.T) {
	contractors := &metadata.Entity{Name: "Контрагенты", Kind: metadata.KindCatalog}
	doc := &metadata.Entity{
		Name:    "Сделка",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Контрагент", Type: "reference:Контрагенты", RefEntity: "Контрагенты"},
		},
	}
	reg := &metadata.Register{
		Name:       "СделкиПоКонтрагентам",
		Dimensions: []metadata.Field{{Name: "Контрагент", Type: "reference:Контрагенты", RefEntity: "Контрагенты"}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{contractors, doc})
	if err := s.store.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}
	posting := mustParse(t, `Процедура ОбработкаПроведения()
	Дв = Движения.СделкиПоКонтрагентам.Добавить();
	Дв.Контрагент = this.Контрагент;
	Дв.Количество = 1;
КонецПроцедуры`)
	s.reg.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{contractors, doc},
		Programs:  map[string]*ast.Program{doc.Name: posting},
		Registers: []*metadata.Register{reg},
	})

	res, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc,
		ID:     uuid.New(),
		IsNew:  true,
		Fields: map[string]any{"Номер": "СД-00001"},
		Action: "post",
	})
	if err != nil {
		t.Fatalf("проведение с пустой ссылкой вернуло техническую ошибку: %v", err)
	}
	if res.DSLError != "" {
		t.Fatalf("проведение с пустой ссылкой вернуло DSL-ошибку: %s", res.DSLError)
	}

	rows, err := s.store.GetMovements(ctx, reg.Name, reg, storage.RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ожидалось одно движение, получено %d", len(rows))
	}
	contractor := rows[0]["Контрагент"]
	if contractor != nil {
		t.Fatalf("пустая ссылка записана как %T(%v), ожидался SQL NULL", contractor, contractor)
	}
}

// formTpProxy.Добавить добавляет строку в obj.TablePartRows и возвращает
// *interpreter.MapThis, в которую DSL присвоит .Количество и .Цена.
func TestFormTpProxy_AddRowModifiesObject(t *testing.T) {
	obj := &runtime.Object{
		TablePartRows: map[string][]map[string]any{},
	}
	tp := &formTpProxy{obj: obj, tpName: "Товары"}

	res := tp.CallMethod("добавить", nil)
	row, ok := res.(*interpreter.MapThis)
	if !ok {
		t.Fatalf("CallMethod(\"добавить\") = %T, ожидался *MapThis", res)
	}
	row.Set("Количество", float64(5))

	if len(obj.TablePartRows["Товары"]) != 1 {
		t.Fatalf("ожидалась 1 строка ТЧ, получено %d", len(obj.TablePartRows["Товары"]))
	}
	// MapThis.Set делает strings.ToLower(name) — проверяем по lowercase ключу.
	if obj.TablePartRows["Товары"][0]["количество"] != float64(5) {
		t.Errorf("после Set(\"Количество\", 5) row[количество] = %v, ожидалось 5", obj.TablePartRows["Товары"][0]["количество"])
	}
}

func TestFormObjectThis_ValueTableAddUsesCanonicalColumnMetadata(t *testing.T) {
	form := &metadata.FormModule{Attributes: []*metadata.FormAttribute{{
		Name: "Подбор", TypeRef: "ValueTable",
		Columns: []*metadata.FormAttributeColumn{
			{Name: "Номенклатура", TypeRef: "string"},
			{Name: "Количество", TypeRef: "number"},
		},
	}}}
	obj := &runtime.Object{TablePartRows: map[string][]map[string]any{}}
	this := &formObjectThis{obj: obj, form: form}
	proxy, ok := this.Get("подбор").(*formTpProxy)
	if !ok {
		t.Fatalf("Get(ValueTable) = %T, ожидался *formTpProxy", this.Get("подбор"))
	}
	row, ok := proxy.CallMethod("Добавить", nil).(interpreter.This)
	if !ok {
		t.Fatalf("Добавить() = %T, ожидался interpreter.This", proxy.CallMethod("Добавить", nil))
	}
	row.Set("количество", float64(5))

	got := obj.TablePartRows["Подбор"]
	if len(got) != 1 || got[0]["Количество"] != float64(5) {
		t.Fatalf("ValueTable row = %#v, ожидалась каноническая колонка Количество=5", got)
	}
	if _, lower := got[0]["количество"]; lower {
		t.Fatalf("ValueTable row сохранил lowercase-дубликат: %#v", got[0])
	}
}

// Очистить должен сбросить ТЧ к nil (а не оставить старые строки).
func TestFormTpProxy_Clear(t *testing.T) {
	obj := &runtime.Object{
		TablePartRows: map[string][]map[string]any{
			"Товары": {{"x": 1}, {"x": 2}},
		},
	}
	tp := &formTpProxy{obj: obj, tpName: "Товары"}
	tp.CallMethod("очистить", nil)
	if len(obj.TablePartRows["Товары"]) != 0 {
		t.Errorf("после Очистить() ожидался пустой список, получено %d строк", len(obj.TablePartRows["Товары"]))
	}
}

// Количество возвращает float64 (DSL числа — float64) — без этого
// сравнение `Объект.Товары.Количество() = 0` всегда было бы false.
func TestFormTpProxy_Count(t *testing.T) {
	obj := &runtime.Object{
		TablePartRows: map[string][]map[string]any{
			"Товары": {{"x": 1}, {"x": 2}, {"x": 3}},
		},
	}
	tp := &formTpProxy{obj: obj, tpName: "Товары"}
	got := tp.CallMethod("количество", nil)
	if got != float64(3) {
		t.Errorf("Количество() = %v (%T), ожидалось 3.0", got, got)
	}
}

// IterateRows — для `Для Каждого Стр Из Объект.Товары Цикл` интерпретатор
// должен видеть []map[string]any. Этот тест защищает интерфейс от случайной
// сигнатуры (например, если поменяют на []any).
func TestFormTpProxy_IterateRows(t *testing.T) {
	rows := []map[string]any{{"a": 1}, {"a": 2}}
	obj := &runtime.Object{TablePartRows: map[string][]map[string]any{"Товары": rows}}
	tp := &formTpProxy{obj: obj, tpName: "Товары"}

	got := tp.IterateRows()
	if len(got) != 2 || got[0]["a"] != 1 || got[1]["a"] != 2 {
		t.Errorf("IterateRows() = %v, ожидалось %v", got, rows)
	}
}
