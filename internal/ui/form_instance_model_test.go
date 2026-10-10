package ui_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/ui"
)

func structureForm(t *testing.T, body string) (*metadata.FormModule, *metadata.Entity) {
	t.Helper()
	source, err := os.ReadFile("testdata/runtime_structure.form.os")
	if err != nil {
		t.Fatal(err)
	}
	program, err := parser.New(lexer.New(string(source)+"\nПроцедура Проверить()\n"+body+"\nКонецПроцедуры", "runtime_structure.form.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	form := &metadata.FormModule{LayoutKind: metadata.FormLayoutManaged, ProgramAST: program,
		Elements: []*metadata.FormElement{{ID: "static", Name: "Статика", Kind: metadata.FormElementGroupBox, TitleMap: map[string]string{"ru": "Статика", "en": "Static"}, Visible: true, Enabled: true,
			Props:        map[string]any{"nested": map[string]any{"items": []any{map[string]any{"key": "source"}}}},
			ChoiceFilter: []metadata.FormChoiceCondition{{Field: "x", Value: new(bool)}}, UnknownXML: []byte("xml"),
			Options: []metadata.FormOption{{}}, Children: []*metadata.FormElement{{ID: "child", Name: "ИсходноеПоле", Kind: metadata.FormElementField, DataPath: "Объект.Комментарий"}}}},
		Attributes: []*metadata.FormAttribute{{Name: "Заметка", TypeRef: "string"}, {Name: "Результат", TypeRef: "ValueTable", Columns: []*metadata.FormAttributeColumn{{Name: "Цена"}}}},
	}
	entity := &metadata.Entity{Name: "Тест", Fields: []metadata.Field{{Name: "Комментарий", Type: metadata.FieldTypeString}}, TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}}}}
	return form, entity
}
func structureModel(t *testing.T, form *metadata.FormModule, entity *metadata.Entity) *ui.ManagedFormRuntime {
	t.Helper()
	m, err := ui.NewManagedFormRuntime(form, entity)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func structureSnapshot(t *testing.T, m *ui.ManagedFormRuntime) []*metadata.FormElement {
	t.Helper()
	s, err := m.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func structureRun(t *testing.T, m *ui.ManagedFormRuntime, name string) []ui.ManagedFormOperation {
	t.Helper()
	ops, err := m.Run(name, interpreter.New(), nil, interpreter.SandboxProfile{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ops
}

func TestManagedFormRuntimeDSLTransaction(t *testing.T) {
	form, entity := structureForm(t, "")
	m := structureModel(t, form, entity)
	before := structureSnapshot(t, m)
	ops := structureRun(t, m, "Собрать")
	if len(ops) != 10 || ops[0].Op != "insert" || ops[0].Position != 0 || ops[0].ParentID != "root" {
		t.Fatalf("operations: %+v", ops)
	}
	s := structureSnapshot(t, m)
	field := s[0].Children[0]
	if field.Name != "Комментарий" || field.DataPath != "Объект.Комментарий" || field.Visible || field.Enabled || !field.ReadOnly || field.Handlers[metadata.FormEventOnChange] != "Изменён" {
		t.Fatalf("field: %+v", field)
	}
	if ops[2].Element.Name != "Черновик" {
		t.Fatal("insert payload changed after rename")
	}
	identity := field.ID
	ops[0].Element.Name = "corrupt"
	structureRun(t, m, "Изменить")
	s = structureSnapshot(t, m)
	if len(s) != 2 || s[1].ID != identity || !s[1].Visible || !s[1].Enabled || s[1].ReadOnly || s[1].TitleMap["ru"] != "Обновлённый комментарий" {
		t.Fatalf("moved field: %+v", s)
	}
	structureRun(t, m, "Повторить")
	if len(form.Elements) != 1 || form.Elements[0].ID != "static" || form.Elements[0].Children[0].Name != "ИсходноеПоле" {
		t.Fatal("source FormModule changed")
	}
	// Every nested payload and returned snapshot must be independent.
	s[0].Props["nested"].(map[string]any)["items"].([]any)[0].(map[string]any)["key"] = "bad"
	s[0].TitleMap["en"] = "bad"
	*s[0].ChoiceFilter[0].Value = true
	s[0].UnknownXML[0] = '!'
	if !reflect.DeepEqual(before[0], structureSnapshot(t, m)[0]) {
		t.Fatal("snapshot shares nested data with model")
	}
	if form.Elements[0].Props["nested"].(map[string]any)["items"].([]any)[0].(map[string]any)["key"] != "source" || *form.Elements[0].ChoiceFilter[0].Value {
		t.Fatal("source shares nested data")
	}
}

func TestManagedFormRuntimeDSLRejectsAndRollsBack(t *testing.T) {
	tests := map[string]string{
		"unknown kind":          `ЭтаФорма.Элементы.Добавить("X", "HTML", ЭтаФорма.Элементы.Корень);`,
		"empty kind":            `ЭтаФорма.Элементы.Добавить("X", "", ЭтаФорма.Элементы.Корень);`,
		"duplicate":             `ЭтаФорма.Элементы.Добавить("статика", "Кнопка", ЭтаФорма.Элементы.Корень);`,
		"container":             `ЭтаФорма.Элементы.Добавить("X", "Кнопка", ЭтаФорма.Элементы.ИсходноеПоле);`,
		"cycle":                 `ЭтаФорма.Элементы.Переместить(ЭтаФорма.Элементы.Корень, ЭтаФорма.Элементы.Статика);`,
		"self cycle":            `ЭтаФорма.Элементы.Переместить(ЭтаФорма.Элементы.Статика, ЭтаФорма.Элементы.Статика);`,
		"fraction position":     `ЭтаФорма.Элементы.Переместить(ЭтаФорма.Элементы.Статика, ЭтаФорма.Элементы.Корень, 0.5);`,
		"rounded fraction":      `ЭтаФорма.Элементы.Добавить("X", "Кнопка", ЭтаФорма.Элементы.Корень, 0.0000000000000000000000000000001);`,
		"ancestor cycle":        `Г = ЭтаФорма.Элементы.Добавить("X", "ГруппаФормы", ЭтаФорма.Элементы.Статика); ЭтаФорма.Элементы.Переместить(ЭтаФорма.Элементы.Статика, Г);`,
		"position bounds":       `ЭтаФорма.Элементы.Добавить("X", "Кнопка", ЭтаФорма.Элементы.Корень, -1);`,
		"frozen name":           `ЭтаФорма.Элементы.Статика.Имя = "Changed";`,
		"frozen kind":           `ЭтаФорма.Элементы.Статика.Вид = "Надпись";`,
		"frozen path":           `ЭтаФорма.Элементы.ИсходноеПоле.ПутьКДанным = "Объект.Комментарий";`,
		"unknown path":          `П = ЭтаФорма.Элементы.Добавить("X", "ПолеВвода", ЭтаФорма.Элементы.Корень); П.ПутьКДанным = "Объект.НетПоля";`,
		"unknown event":         `ЭтаФорма.Элементы.ИсходноеПоле.УстановитьДействие("JavaScript", "Изменён");`,
		"missing AST procedure": `ЭтаФорма.Элементы.ИсходноеПоле.УстановитьДействие("ПриИзменении", "НетПроцедуры");`,
		"removed subtree":       `П = ЭтаФорма.Элементы.ИсходноеПоле; ЭтаФорма.Элементы.Удалить(ЭтаФорма.Элементы.Статика); П.Заголовок = "stale";`,
		"replacement stale":     `П = ЭтаФорма.Элементы.ИсходноеПоле; ЭтаФорма.Элементы.Удалить(П); ЭтаФорма.Элементы.Добавить("ИсходноеПоле", "Кнопка", ЭтаФорма.Элементы.Корень); П.Заголовок = "stale";`,
		"root remove":           `ЭтаФорма.Элементы.Удалить(ЭтаФорма.Элементы.Корень);`,
		"strict boolean":        `ЭтаФорма.Элементы.Статика.Видимость = "true";`,
		"immutable ID":          `ЭтаФорма.Элементы.Статика.ID = "chosen";`,
		"exception":             `ВызватьИсключение "failure";`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			form, entity := structureForm(t, `ЭтаФорма.Элементы.Статика.Заголовок = "partial";`+body)
			m := structureModel(t, form, entity)
			before := structureSnapshot(t, m)
			ops, err := m.Run("Проверить", interpreter.New(), nil, interpreter.SandboxProfile{}, nil)
			if err == nil || len(ops) != 0 {
				t.Fatalf("expected rollback, ops=%+v err=%v", ops, err)
			}
			if !reflect.DeepEqual(before, structureSnapshot(t, m)) {
				t.Fatal("partial structural commit")
			}
		})
	}
}

func TestManagedFormRuntimeDSLDeclaredPaths(t *testing.T) {
	for _, path := range []string{"объект.комментарий", "Объект.Строки", "Объект.Строки.Количество", "Заметка", "Форма.Заметка", "Результат.Цена", "Форма.Результат.Цена"} {
		t.Run(path, func(t *testing.T) {
			form, entity := structureForm(t, `П = ЭтаФорма.Элементы.Добавить("X", "ПолеВвода", ЭтаФорма.Элементы.Корень); П.ПутьКДанным = "`+path+`";`)
			m := structureModel(t, form, entity)
			structureRun(t, m, "Проверить")
			if structureSnapshot(t, m)[1].DataPath != path {
				t.Fatal("declared path lost")
			}
		})
	}
}

func TestManagedFormRuntimeDSLLimits(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		profile    interpreter.SandboxProfile
		vars       map[string]any
	}{
		{"operations", `Для i = 1 По 65 Цикл ЭтаФорма.Элементы.Статика.Заголовок = "X"; КонецЦикла;`, interpreter.SandboxProfile{}, nil},
		{"caught limit", `Попытка Для i = 1 По 65 Цикл ЭтаФорма.Элементы.Статика.Заголовок = "X"; КонецЦикла; Исключение Поймано = Истина; КонецПопытки;`, interpreter.SandboxProfile{}, nil},
		{"patch", `ЭтаФорма.Элементы.Статика.Заголовок = БольшойТекст;`, interpreter.SandboxProfile{}, map[string]any{"БольшойТекст": strings.Repeat("я", ui.ManagedFormMaxPatchBytes)}},
		{"timeout", `ЭтаФорма.Элементы.Статика.Заголовок = "partial"; Пока Истина Цикл КонецЦикла;`, interpreter.SandboxProfile{MaxWallClock: time.Millisecond, MaxLoopIters: 100000000}, nil},
		{"cancelled", `ЭтаФорма.Элементы.Статика.Заголовок = "partial";`, interpreter.SandboxProfile{Context: cancelledStructureContext()}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form, entity := structureForm(t, tc.body)
			m := structureModel(t, form, entity)
			before := structureSnapshot(t, m)
			ops, err := m.Run("Проверить", interpreter.New(), nil, tc.profile, tc.vars)
			if err == nil || len(ops) != 0 || !reflect.DeepEqual(before, structureSnapshot(t, m)) {
				t.Fatalf("limit did not rollback: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		count int
	}{{"nodes", ui.ManagedFormMaxNodes}, {"depth", ui.ManagedFormMaxDepth}} {
		t.Run(tc.name, func(t *testing.T) {
			form, entity := structureForm(t, `ЭтаФорма.Элементы.Добавить("Extra", "ГруппаФормы", ЭтаФорма.Элементы.Найти("Last"));`)
			form.Elements = nil
			if tc.name == "nodes" {
				for i := 0; i < tc.count; i++ {
					form.Elements = append(form.Elements, &metadata.FormElement{Name: fmt.Sprintf("N%d", i), Kind: metadata.FormElementGroupBox})
				}
				form.Elements[tc.count-1].Name = "Last"
			} else {
				var previous *metadata.FormElement
				for i := 0; i < tc.count; i++ {
					n := &metadata.FormElement{Name: fmt.Sprintf("N%d", i), Kind: metadata.FormElementGroupBox}
					if previous == nil {
						form.Elements = append(form.Elements, n)
					} else {
						previous.Children = append(previous.Children, n)
					}
					previous = n
				}
				previous.Name = "Last"
			}
			m := structureModel(t, form, entity)
			before := structureSnapshot(t, m)
			if _, err := m.Run("Проверить", interpreter.New(), nil, interpreter.SandboxProfile{}, nil); err == nil {
				t.Fatal("limit accepted")
			}
			if !reflect.DeepEqual(before, structureSnapshot(t, m)) {
				t.Fatal("limit partially committed")
			}
		})
	}
}
func cancelledStructureContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestManagedFormRuntimeDSLParallelIsolation(t *testing.T) {
	form, entity := structureForm(t, "")
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := ui.NewManagedFormRuntime(form, entity)
			if err != nil {
				errs <- err
				return
			}
			if _, err = m.Run("Собрать", interpreter.New(), nil, interpreter.SandboxProfile{}, nil); err != nil {
				errs <- err
				return
			}
			s, err := m.Snapshot()
			if err != nil {
				errs <- err
				return
			}
			ids <- s[0].ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatal("shared server ID")
		}
		seen[id] = true
	}
	if len(seen) != 16 || len(form.Elements) != 1 || form.Elements[0].TitleMap["ru"] != "Статика" {
		t.Fatal("instances changed shared metadata")
	}
}

func TestManagedFormRuntimeConstructorRejectsUnsafeTree(t *testing.T) {
	for _, name := range []string{"cycle", "shared", "non-container", "duplicate", "opaque", "props cycle", "autogen"} {
		t.Run(name, func(t *testing.T) {
			form, entity := structureForm(t, "")
			n := form.Elements[0]
			switch name {
			case "cycle":
				n.Children = append(n.Children, n)
			case "shared":
				form.Elements = append(form.Elements, n)
			case "non-container":
				n.Kind = metadata.FormElementField
			case "duplicate":
				n.Children[0].Name = "статика"
			case "opaque":
				n.Props["func"] = func() {}
			case "props cycle":
				n.Props["cycle"] = n.Props
			case "autogen":
				form.LayoutKind = metadata.FormLayoutAutogen
			}
			if _, err := ui.NewManagedFormRuntime(form, entity); err == nil {
				t.Fatal("unsafe tree accepted")
			}
		})
	}
}

type structureReferenceSink struct{ value any }

func (s *structureReferenceSink) Get(string) any          { return s.value }
func (s *structureReferenceSink) Set(_ string, value any) { s.value = value }

func TestManagedFormRuntimeDSLExpiredAndForeignReferences(t *testing.T) {
	form, entity := structureForm(t, `Приёмник.Ссылка = ЭтаФорма.Элементы.Статика;`)
	m := structureModel(t, form, entity)
	sink := &structureReferenceSink{}
	if _, err := m.Run("Проверить", interpreter.New(), nil, interpreter.SandboxProfile{}, map[string]any{"Приёмник": sink}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`ЭтаФорма.Элементы.Статика.Заголовок = "partial"; ЭтаФорма.Элементы.Удалить(Приёмник.Ссылка);`,
		`Приёмник.Ссылка.Заголовок = "expired";`,
	} {
		otherForm, otherEntity := structureForm(t, body)
		other := structureModel(t, otherForm, otherEntity)
		before := structureSnapshot(t, other)
		ops, err := other.Run("Проверить", interpreter.New(), nil, interpreter.SandboxProfile{}, map[string]any{"Приёмник": sink})
		if err == nil || len(ops) != 0 || !reflect.DeepEqual(before, structureSnapshot(t, other)) {
			t.Fatalf("foreign/expired reference accepted: %v", err)
		}
	}
	// A captured reference cannot mutate the committed model either.
	if structureSnapshot(t, m)[0].Title != "" {
		t.Fatal("expired reference changed its original model")
	}
}

func TestManagedFormRuntimeDSLBoundaryAndSameParentMove(t *testing.T) {
	form, entity := structureForm(t, `Для i = 1 По 64 Цикл ЭтаФорма.Элементы.Статика.Заголовок = "X"; КонецЦикла;`)
	m := structureModel(t, form, entity)
	if ops := structureRun(t, m, "Проверить"); len(ops) != ui.ManagedFormMaxOperations {
		t.Fatal("exact operation limit rejected")
	}
	form, entity = structureForm(t, `
		А = ЭтаФорма.Элементы.Добавить("A", "Кнопка", ЭтаФорма.Элементы.Корень);
		Б = ЭтаФорма.Элементы.Добавить("B", "Кнопка", ЭтаФорма.Элементы.Корень);
		ЭтаФорма.Элементы.Переместить(Б, ЭтаФорма.Элементы.Корень, 0);
		ЭтаФорма.Элементы.Переместить(Б, ЭтаФорма.Элементы.Корень);
	`)
	m = structureModel(t, form, entity)
	structureRun(t, m, "Проверить")
	s := structureSnapshot(t, m)
	if len(s) != 3 || s[0].Name != "Статика" || s[1].Name != "A" || s[2].Name != "B" {
		t.Fatal("same-parent move lost/repeated/reordered nodes")
	}
	form, entity = structureForm(t, `ЭтаФорма.Элементы.Добавить(НовоеИмя, "Кнопка", ЭтаФорма.Элементы.Корень);`)
	m = structureModel(t, form, entity)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	// One shared model serializes concurrent handlers while sharing a read-only
	// interpreter; each successful run returns its own independent diff.
	interp := interpreter.New()
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := m.Run("Проверить", interp, nil, interpreter.SandboxProfile{}, map[string]any{"НовоеИмя": fmt.Sprintf("Parallel%d", index)})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if result := structureSnapshot(t, m); len(result) != 17 || result[0].Name != "Статика" || len(form.Elements) != 1 {
		t.Fatal("serialized handlers lost changes or changed source metadata")
	}
}

// Keep the source handler alive while another instance attempts direct proxy
// dispatch; checking only collection arguments misses these accesses.
type structureLiveReferenceSink struct {
	reference chan any
	release   chan struct{}
}

func (s *structureLiveReferenceSink) Get(string) any { return nil }
func (s *structureLiveReferenceSink) Set(_ string, value any) {
	s.reference <- value
	<-s.release
}

func TestManagedFormRuntimeDSLLiveForeignProxyAccess(t *testing.T) {
	for _, tc := range []struct{ name, capture, access string }{
		{"form get", "ЭтаФорма", `Значение = Чужой.Элементы;`},
		{"form set", "ЭтаФорма", `Чужой.Элементы = Неопределено;`},
		{"collection get", "ЭтаФорма.Элементы", `Значение = Чужой.Статика;`},
		{"collection set", "ЭтаФорма.Элементы", `Чужой.Статика = Неопределено;`},
		{"collection method", "ЭтаФорма.Элементы", `Значение = Чужой.Найти("Статика");`},
		{"element get", "ЭтаФорма.Элементы.Статика", `Значение = Чужой.Заголовок;`},
		{"element set", "ЭтаФорма.Элементы.Статика", `Чужой.Заголовок = "foreign";`},
		{"element method", "ЭтаФорма.Элементы.Статика", `Чужой.УстановитьДействие("ПриИзменении", "Изменён");`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sourceForm, entity := structureForm(t, `Приёмник.Ссылка = `+tc.capture+`; ЭтаФорма.Элементы.Статика.Заголовок = "owner";`)
			source := structureModel(t, sourceForm, entity)
			sink := &structureLiveReferenceSink{reference: make(chan any), release: make(chan struct{})}
			done := make(chan error, 1)
			go func() {
				_, err := source.Run("Проверить", interpreter.New(), nil, interpreter.SandboxProfile{}, map[string]any{"Приёмник": sink})
				done <- err
			}()
			reference := <-sink.reference
			defer func() {
				close(sink.release)
				if err := <-done; err != nil {
					t.Errorf("foreign access poisoned source handler: %v", err)
				}
				if got := structureSnapshot(t, source)[0].Title; got != "owner" {
					t.Errorf("source handler did not commit its own change: %q", got)
				}
			}()
			form, entity := structureForm(t, `ЭтаФорма.Элементы.Статика.Заголовок = "partial";
Попытка
`+tc.access+`
Исключение
 Приёмник.Ссылка = Истина;
КонецПопытки;`)
			other := structureModel(t, form, entity)
			before := structureSnapshot(t, other)
			caught := &structureReferenceSink{}
			ops, err := other.Run("Проверить", interpreter.New(), nil, interpreter.SandboxProfile{}, map[string]any{"Чужой": reference, "Приёмник": caught})
			if caught.value != true {
				t.Error("foreign access did not raise a catchable error")
			}
			if err == nil || !strings.Contains(err.Error(), "другому экземпляру или обработчику") || len(ops) != 0 || !reflect.DeepEqual(before, structureSnapshot(t, other)) {
				t.Fatalf("foreign access did not roll back current handler: ops=%+v err=%v", ops, err)
			}
		})
	}
}

func TestManagedFormRuntimeDSLCaughtExpiredProxyRollsBack(t *testing.T) {
	for _, tc := range []struct{ name, capture, access string }{
		{"form", "ЭтаФорма", `Значение = Приёмник.Ссылка.Элементы;`},
		{"collection get", "ЭтаФорма.Элементы", `Значение = Приёмник.Ссылка.Статика;`},
		{"collection method", "ЭтаФорма.Элементы", `Значение = Приёмник.Ссылка.Найти("Статика");`},
		{"element get", "ЭтаФорма.Элементы.Статика", `Значение = Приёмник.Ссылка.Заголовок;`},
		{"element set", "ЭтаФорма.Элементы.Статика", `Приёмник.Ссылка.Заголовок = "expired";`},
		{"element method", "ЭтаФорма.Элементы.Статика", `Приёмник.Ссылка.УстановитьДействие("ПриИзменении", "Изменён");`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form, entity := structureForm(t, `Если Сохранить Тогда
 Приёмник.Ссылка = `+tc.capture+`;
Иначе
 ЭтаФорма.Элементы.Статика.Заголовок = "partial";
 Попытка
 `+tc.access+`
 Исключение
  Поймано.Ссылка = Истина;
 КонецПопытки;
КонецЕсли;`)
			m := structureModel(t, form, entity)
			sink, caught := &structureReferenceSink{}, &structureReferenceSink{}
			vars := map[string]any{"Сохранить": true, "Приёмник": sink, "Поймано": caught}
			if _, err := m.Run("Проверить", interpreter.New(), nil, interpreter.SandboxProfile{}, vars); err != nil {
				t.Fatal(err)
			}
			before := structureSnapshot(t, m)
			vars["Сохранить"] = false
			ops, err := m.Run("Проверить", interpreter.New(), nil, interpreter.SandboxProfile{}, vars)
			if caught.value != true {
				t.Error("expired access did not raise a catchable error")
			}
			if err == nil || len(ops) != 0 || !reflect.DeepEqual(before, structureSnapshot(t, m)) {
				t.Fatalf("caught expired access committed partial structure: ops=%+v err=%v", ops, err)
			}
		})
	}
}
