package entityservice

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func equalFoldFieldState(fields map[string]any, name string) map[string]any {
	state := make(map[string]any)
	for key, value := range fields {
		if strings.EqualFold(key, name) {
			state[key] = value
		}
	}
	return state
}

// Автонумерация — свойство записи объекта, а не конкретного входа (#869).
// Форма, ИИ и REST v1/v2 сохраняют через Service.Save; прямой docWriter
// вызывает ту же EnsureAutoNumber внутри своей транзакции.
func numberedCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name:      "Контрагенты",
		Kind:      metadata.KindCatalog,
		Numerator: &metadata.Numerator{Prefix: "К-", Length: 6},
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
}

func legacyNumberedDocument() *metadata.Entity {
	return &metadata.Entity{
		Name: "ЗаказПокупателя",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
	}
}

func newNumberingService(t *testing.T, db *storage.DB, ents []*metadata.Entity) *Service {
	t.Helper()
	if err := db.Migrate(context.Background(), ents); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: ents})
	return &Service{Store: db, Reg: registry, Interp: interpreter.New()}
}

func TestEnsureAutoNumber_ВнеТранзакцииFailClosedMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		cat := numberedCatalog()
		svc := newNumberingService(t, db, []*metadata.Entity{cat})
		fields := map[string]any{"Код": "", "КОД": nil}
		before := equalFoldFieldState(fields, metadata.StandardCodeField)

		err := svc.EnsureAutoNumber(context.Background(), cat, &runtime.Object{Fields: fields})
		if !errors.Is(err, ErrAutoNumberRequiresTx) {
			t.Fatalf("EnsureAutoNumber error = %v, ожидался ErrAutoNumberRequiresTx", err)
		}
		if after := equalFoldFieldState(fields, metadata.StandardCodeField); !reflect.DeepEqual(after, before) {
			t.Fatalf("вызов вне tx изменил map: до=%#v после=%#v", before, after)
		}

		withoutNumerator := numberedCatalog()
		withoutNumerator.Numerator = nil
		if err := svc.EnsureAutoNumber(context.Background(), withoutNumerator, &runtime.Object{}); !errors.Is(err, ErrAutoNumberRequiresTx) {
			t.Fatalf("даже no-op EnsureAutoNumber обязан fail-closed вне tx, error=%v", err)
		}
	})
}

func TestSave_НумеруетСправочникMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := numberedCatalog()
		svc := newNumberingService(t, db, []*metadata.Entity{cat})

		tests := []struct {
			name       string
			fields     map[string]any
			want       string
			presentKey string
			absentKey  string
		}{
			{name: "поле отсутствует", fields: map[string]any{"Наименование": "A"}, want: "К-000001", presentKey: "код", absentKey: "Код"},
			{name: "Pascal-case пустая строка", fields: map[string]any{"Код": "", "Наименование": "B"}, want: "К-000002", presentKey: "Код", absentKey: "код"},
			{name: "lowercase пустая строка", fields: map[string]any{"код": "", "Наименование": "C"}, want: "К-000003", presentKey: "код", absentKey: "Код"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				id := uuid.New()
				result, err := svc.Save(ctx, SaveRequest{
					Entity: cat, ID: id, IsNew: true, Fields: tc.fields,
				})
				if err != nil {
					t.Fatalf("Save: %v", err)
				}
				if result.DSLError != "" {
					t.Fatalf("Save вернул DSL-ошибку: %s", result.DSLError)
				}

				row, err := db.GetByID(ctx, cat.Name, id, cat)
				if err != nil {
					t.Fatal(err)
				}
				if got := fmt.Sprint(row[metadata.StandardCodeField]); got != tc.want {
					t.Fatalf("код в БД = %q, ожидался %q; строка: %v", got, tc.want, row)
				}
				if got := fmt.Sprint(tc.fields[tc.presentKey]); got != tc.want {
					t.Fatalf("%s в исходной map = %q, ожидался %q", tc.presentKey, got, tc.want)
				}
				if _, duplicate := tc.fields[tc.absentKey]; duplicate {
					t.Fatalf("Object.Set создал дублирующий ключ %q: %v", tc.absentKey, tc.fields)
				}
				if version, err := db.EntityVersion(ctx, cat.Name, id); err != nil {
					t.Fatalf("EntityVersion: %v", err)
				} else if version != 1 {
					t.Fatalf("версия новой записи = %d, ожидалась 1", version)
				}
			})
		}
	})
}

// Документ без блока numerator сохраняет прежний контракт: стандартный Номер
// выдаётся из legacy-счётчика и форматируется шестью цифрами.
func TestSave_LegacyДокументБезNumeratorMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := legacyNumberedDocument()
		svc := newNumberingService(t, db, []*metadata.Entity{doc})
		fields := map[string]any{"Номер": "", "Комментарий": "пустой номер из REST"}
		id := uuid.New()

		if _, err := svc.Save(ctx, SaveRequest{Entity: doc, ID: id, IsNew: true, Fields: fields}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		row, err := db.GetByID(ctx, doc.Name, id, doc)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(row["Номер"]); got != "000001" {
			t.Fatalf("legacy Номер = %q, ожидался 000001; строка: %v", got, row)
		}
		if got := fmt.Sprint(fields["Номер"]); got != "000001" {
			t.Fatalf("Pascal-case Номер в исходной map = %q, ожидался 000001", got)
		}
		if _, duplicate := fields["номер"]; duplicate {
			t.Fatalf("рядом с Pascal-case Номер появился lowercase-дубликат: %v", fields)
		}
		if version, err := db.EntityVersion(ctx, doc.Name, id); err != nil {
			t.Fatalf("EntityVersion: %v", err)
		} else if version != 1 {
			t.Fatalf("версия нового документа = %d, ожидалась 1", version)
		}
	})
}

// Явно заданный код не переписывается и не расходует значение счётчика.
func TestSave_ЯвныйКодСохраняетсяMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := numberedCatalog()
		svc := newNumberingService(t, db, []*metadata.Entity{cat})

		manualID := uuid.New()
		if _, err := svc.Save(ctx, SaveRequest{
			Entity: cat, ID: manualID, IsNew: true,
			Fields: map[string]any{metadata.StandardCodeField: "РУЧНОЙ-1", "Наименование": "X"},
		}); err != nil {
			t.Fatalf("Save ручного кода: %v", err)
		}
		row, err := db.GetByID(ctx, cat.Name, manualID, cat)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(row[metadata.StandardCodeField]); got != "РУЧНОЙ-1" {
			t.Fatalf("явно заданный код заменён на %q", got)
		}

		autoID := uuid.New()
		if _, err := svc.Save(ctx, SaveRequest{
			Entity: cat, ID: autoID, IsNew: true,
			Fields: map[string]any{metadata.StandardCodeField: "", "Наименование": "Y"},
		}); err != nil {
			t.Fatalf("Save автоматического кода: %v", err)
		}
		autoRow, err := db.GetByID(ctx, cat.Name, autoID, cat)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(autoRow[metadata.StandardCodeField]); got != "К-000001" {
			t.Fatalf("ручной код израсходовал счётчик: следующий код = %q", got)
		}
	})
}

// Справочник БЕЗ нумератора код не получает: раздача кодов всем существующим
// конфигурациям была бы несовместимым изменением данных.
func TestSave_БезНумератораКодНеВыдаётсяMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := numberedCatalog()
		cat.Numerator = nil
		svc := newNumberingService(t, db, []*metadata.Entity{cat})

		id := uuid.New()
		if _, err := svc.Save(ctx, SaveRequest{
			Entity: cat, ID: id, IsNew: true,
			Fields: map[string]any{"Наименование": "X"},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		row, err := db.GetByID(ctx, cat.Name, id, cat)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(row[metadata.StandardCodeField]); got != "<nil>" && strings.TrimSpace(got) != "" {
			t.Fatalf("без numerator: выдан код %q", got)
		}
	})
}

// Ошибка самого генератора обязательного кода не должна превращаться в
// успешную запись с NULL/пустой строкой.
func TestSave_ОшибкаGenerateNumberОтменяетЗаписьMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := numberedCatalog()
		svc := newNumberingService(t, db, []*metadata.Entity{cat})
		if _, err := db.Exec(ctx, "DROP TABLE _numerators"); err != nil {
			t.Fatalf("DROP _numerators: %v", err)
		}

		id := uuid.New()
		if _, err := svc.Save(ctx, SaveRequest{
			Entity: cat, ID: id, IsNew: true,
			Fields: map[string]any{metadata.StandardCodeField: "", "Наименование": "X"},
		}); err == nil {
			t.Fatal("Save проглотил ошибку GenerateNumber")
		}
		if _, err := db.GetByID(ctx, cat.Name, id, cat); err == nil {
			t.Fatal("объект записан после ошибки GenerateNumber")
		}
	})
}

// Номер, provisional-строка и хук находятся в одной транзакции: исключение
// ПриЗаписи откатывает и объект, и занятое значение счётчика.
func TestSave_ИсключениеХукаОткатываетСчётчикMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := numberedCatalog()
		svc := newNumberingService(t, db, []*metadata.Entity{cat})
		program := mustParseProgramT(t, `
Процедура ПриЗаписи()
	ВызватьИсключение("отказ после выдачи номера");
КонецПроцедуры`)
		svc.Reg.Load(runtime.LoadOptions{
			Entities: []*metadata.Entity{cat},
			Programs: map[string]*ast.Program{cat.Name: program},
		})

		failedID := uuid.New()
		fields := map[string]any{
			metadata.StandardCodeField: "",
			"КОД":                      nil,
			"Наименование":             "повторить ту же map",
		}
		before := equalFoldFieldState(fields, metadata.StandardCodeField)
		result, err := svc.Save(ctx, SaveRequest{
			Entity: cat, ID: failedID, IsNew: true,
			Fields: fields,
		})
		if err != nil {
			t.Fatalf("исключение хука стало инфраструктурной ошибкой: %v", err)
		}
		if !strings.Contains(result.DSLError, "отказ после выдачи номера") {
			t.Fatalf("DSLError = %q", result.DSLError)
		}
		if _, err := db.GetByID(ctx, cat.Name, failedID, cat); err == nil {
			t.Fatal("provisional-строка пережила исключение хука")
		}
		if after := equalFoldFieldState(fields, metadata.StandardCodeField); !reflect.DeepEqual(after, before) {
			t.Fatalf("rollback не восстановил точные EqualFold-ключи: до=%#v после=%#v", before, after)
		}

		// Убираем хук и повторяем Save с тем же ID и той же caller-owned map.
		svc.Reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{cat}})
		if _, err := svc.Save(ctx, SaveRequest{
			Entity: cat, ID: failedID, IsNew: true, Fields: fields,
		}); err != nil {
			t.Fatalf("Save после исключения: %v", err)
		}
		row, err := db.GetByID(ctx, cat.Name, failedID, cat)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(row[metadata.StandardCodeField]); got != "К-000001" {
			t.Fatalf("retry той же map сохранил неверный код = %q", got)
		}
		if version, err := db.EntityVersion(ctx, cat.Name, failedID); err != nil {
			t.Fatalf("EntityVersion: %v", err)
		} else if version != 1 {
			t.Fatalf("версия новой записи после provisional = %d, ожидалась 1", version)
		}

		nextFields := map[string]any{metadata.StandardCodeField: "", "Наименование": "следующий"}
		nextID := uuid.New()
		if _, err := svc.Save(ctx, SaveRequest{Entity: cat, ID: nextID, IsNew: true, Fields: nextFields}); err != nil {
			t.Fatalf("Save следующего объекта: %v", err)
		}
		if got := fmt.Sprint(nextFields[metadata.StandardCodeField]); got != "К-000002" {
			t.Fatalf("следующий код = %q, ожидался К-000002", got)
		}
		if _, duplicate := nextFields["код"]; duplicate {
			t.Fatalf("у следующего объекта появился lowercase-дубликат: %#v", nextFields)
		}
	})
}

// Сбой финальной записи после успешной генерации также обязан откатить
// счётчик: иначе после восстановления хранилища останется необъяснимый разрыв.
func TestSave_ОшибкаStorageПослеНомераОткатываетСчётчикMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := numberedCatalog()
		svc := newNumberingService(t, db, []*metadata.Entity{cat})
		if _, err := db.Exec(ctx, "DROP TABLE "+metadata.TableName(cat.Name)); err != nil {
			t.Fatalf("DROP entity table: %v", err)
		}

		id := uuid.New()
		fields := map[string]any{metadata.StandardCodeField: "", "КОД": nil, "Наименование": "retry"}
		before := equalFoldFieldState(fields, metadata.StandardCodeField)
		if _, err := svc.Save(ctx, SaveRequest{
			Entity: cat, ID: id, IsNew: true, Fields: fields,
		}); err == nil {
			t.Fatal("ожидалась ошибка записи в отсутствующую таблицу")
		}
		if after := equalFoldFieldState(fields, metadata.StandardCodeField); !reflect.DeepEqual(after, before) {
			t.Fatalf("storage rollback не восстановил map: до=%#v после=%#v", before, after)
		}

		if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
			t.Fatalf("восстановление таблицы: %v", err)
		}
		if _, err := svc.Save(ctx, SaveRequest{
			Entity: cat, ID: id, IsNew: true, Fields: fields,
		}); err != nil {
			t.Fatalf("Save после восстановления: %v", err)
		}
		row, err := db.GetByID(ctx, cat.Name, id, cat)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(row[metadata.StandardCodeField]); got != "К-000001" {
			t.Fatalf("storage-сбой не откатил счётчик: первый успешный код = %q", got)
		}
	})
}

func TestSave_PreflightВидитНомерИОткатНеРасходуетЕгоMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := numberedCatalog()
		svc := newNumberingService(t, db, []*metadata.Entity{cat})
		fields := map[string]any{"Код": "", "КОД": nil, "Наименование": "отказ preflight"}
		before := equalFoldFieldState(fields, metadata.StandardCodeField)
		preflightReject := errors.New("preflight reject")

		_, err := svc.Save(ctx, SaveRequest{
			Entity: cat, ID: uuid.New(), IsNew: true, Fields: fields,
			Preflight: func(txCtx context.Context, obj *runtime.Object) error {
				if !storage.HasTx(txCtx) {
					t.Fatal("Preflight вызван вне storage tx")
				}
				if got := fmt.Sprint(obj.Get(metadata.StandardCodeField)); got != "К-000001" {
					t.Fatalf("Preflight видит код %q, ожидался К-000001", got)
				}
				return preflightReject
			},
		})
		if !errors.Is(err, preflightReject) {
			t.Fatalf("Save error = %v, ожидался preflightReject", err)
		}
		if after := equalFoldFieldState(fields, metadata.StandardCodeField); !reflect.DeepEqual(after, before) {
			t.Fatalf("preflight rollback не восстановил map: до=%#v после=%#v", before, after)
		}

		id := uuid.New()
		if _, err := svc.Save(ctx, SaveRequest{Entity: cat, ID: id, IsNew: true, Fields: fields}); err != nil {
			t.Fatalf("retry той же map: %v", err)
		}
		if got := fmt.Sprint(fields[metadata.StandardCodeField]); got != "К-000001" {
			t.Fatalf("отказ preflight израсходовал номер: retry получил %q", got)
		}
	})
}

func TestSave_PreflightReplacementRollbackReconnectsOriginalFieldsMapMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := numberedCatalog()
		svc := newNumberingService(t, db, []*metadata.Entity{cat})
		fields := map[string]any{"Код": "", "КОД": nil, "Наименование": "original"}
		before := equalFoldFieldState(fields, metadata.StandardCodeField)
		replacement := map[string]any{"код": "replacement", "replacement-only": true}
		reject := errors.New("reject replaced fields")
		var captured *runtime.Object

		_, err := svc.Save(ctx, SaveRequest{
			Entity: cat, ID: uuid.New(), IsNew: true, Fields: fields,
			Preflight: func(_ context.Context, obj *runtime.Object) error {
				captured = obj
				obj.Fields = replacement
				return reject
			},
		})
		if !errors.Is(err, reject) {
			t.Fatalf("Save error = %v, expected replacement rejection", err)
		}
		if after := equalFoldFieldState(fields, metadata.StandardCodeField); !reflect.DeepEqual(after, before) {
			t.Fatalf("original map spelling/value state changed: before=%#v after=%#v", before, after)
		}
		if got := equalFoldFieldState(replacement, metadata.StandardCodeField); len(got) != 0 {
			t.Fatalf("rollback left numbered-field state in replacement map: %#v", got)
		}
		if captured == nil {
			t.Fatal("preflight did not capture save object")
		}
		captured.Fields["identity-probe"] = true
		if fields["identity-probe"] != true {
			t.Fatalf("rollback did not reconnect obj.Fields to caller map: obj=%#v caller=%#v", captured.Fields, fields)
		}
		if _, leaked := replacement["identity-probe"]; leaked {
			t.Fatalf("obj.Fields still points at replacement map: %#v", replacement)
		}
	})
}

func TestSave_OuterTxRollbackВосстанавливаетТуЖеMapMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := numberedCatalog()
		svc := newNumberingService(t, db, []*metadata.Entity{cat})
		fields := map[string]any{"Код": "", "КОД": nil, "Наименование": "outer rollback"}
		before := equalFoldFieldState(fields, metadata.StandardCodeField)
		outerReject := errors.New("outer reject")
		id := uuid.New()

		err := db.WithTx(ctx, func(txCtx context.Context) error {
			result, err := svc.Save(txCtx, SaveRequest{Entity: cat, ID: id, IsNew: true, Fields: fields})
			if err != nil {
				return err
			}
			if result.DSLError != "" {
				return fmt.Errorf("unexpected DSLError: %s", result.DSLError)
			}
			if got := fmt.Sprint(fields[metadata.StandardCodeField]); got != "К-000001" {
				return fmt.Errorf("inside outer tx code = %q", got)
			}
			return outerReject
		})
		if !errors.Is(err, outerReject) {
			t.Fatalf("outer tx error = %v", err)
		}
		if after := equalFoldFieldState(fields, metadata.StandardCodeField); !reflect.DeepEqual(after, before) {
			t.Fatalf("outer rollback не восстановил map: до=%#v после=%#v", before, after)
		}
		if _, err := db.GetByID(ctx, cat.Name, id, cat); err == nil {
			t.Fatal("строка пережила outer rollback")
		}

		if _, err := svc.Save(ctx, SaveRequest{Entity: cat, ID: id, IsNew: true, Fields: fields}); err != nil {
			t.Fatalf("retry после outer rollback: %v", err)
		}
		if got := fmt.Sprint(fields[metadata.StandardCodeField]); got != "К-000001" {
			t.Fatalf("outer rollback израсходовал номер: retry получил %q", got)
		}
	})
}
