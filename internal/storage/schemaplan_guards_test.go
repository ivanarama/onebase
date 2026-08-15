package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Сторожа реструктуризации (план 81, разбор в ишью #586).

func numField(id string, length, scale int) metadata.Field {
	return metadata.Field{ID: id, Name: "Сумма", Type: metadata.FieldTypeNumber, Length: length, Scale: scale}
}

// Сужение точности числа теряет данные так же безвозвратно, как удаление
// колонки: на PostgreSQL NUMERIC(15,0) округляет копейки, а checkConvertible
// этого не видит — «10.55» разбирается как число, целевая точность ей неизвестна.
func TestRetypeNarrowingIsDestructive(t *testing.T) {
	cases := []struct {
		name string
		from string
		to   metadata.Field
		want bool
	}{
		{"меньше знаков после запятой", "number(15,2)", numField("f", 15, 0), true},
		{"меньше разрядность целой части", "number(15,2)", numField("f", 5, 2), true},
		{"та же точность", "number(15,2)", numField("f", 15, 2), false},
		// Разрядность в number(L,S) считает ВСЕ знаки, поэтому «добавить копейки,
		// не тронув L» отбирает их у целой части: 15 знаков до запятой было,
		// 13 стало, и большие значения в новый тип уже не влезают.
		{"копейки за счёт целой части", "number(15,0)", numField("f", 15, 2), true},
		{"шире разрядность", "number(5,2)", numField("f", 15, 2), false},
		{"ограничения сняты", "number(15,2)", metadata.Field{ID: "f", Name: "Сумма", Type: metadata.FieldTypeNumber}, false},
		{"смена типа — забота checkConvertible", "string", numField("f", 15, 2), false},
		{"целая часть сохранена при большем масштабе", "number(5,0)", numField("f", 7, 2), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			change := SchemaChange{Kind: ChangeRetype, From: c.from, Field: c.to}
			if got := change.Destructive(); got != c.want {
				t.Errorf("Destructive()=%v, ждали %v (%s → %s)", got, c.want, c.from, metadata.FieldSignature(c.to))
			}
		})
	}
}

// Без разрешения сужение не применяется, а колонка и данные остаются прежними:
// молча округлить деньги хуже, чем не применить изменение.
func TestRestructureNarrowingSkippedWithoutPermission(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	before := catalogWithField(numField("f_sum", 15, 2))
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, before.Name, id, map[string]any{"Наименование": "Гвозди", "Сумма": "10.55"}, before); err != nil {
		t.Fatal(err)
	}

	after := catalogWithField(numField("f_sum", 15, 0))
	var skipped []SchemaChange
	db.SetSchemaOptions(SchemaOptions{Report: func(c SchemaChange, applied bool) {
		if !applied {
			skipped = append(skipped, c)
		}
	}})
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(skipped) != 1 || skipped[0].Kind != ChangeRetype {
		t.Fatalf("сужение должно быть отложено и попасть в отчёт, отложено: %+v", skipped)
	}
	if !strings.Contains(skipped[0].Note, "вмещает меньше") {
		t.Errorf("примечание не объясняет потерю: %q", skipped[0].Note)
	}
	var got *string
	if err := db.QueryRow(ctx, `SELECT CAST(сумма AS TEXT) FROM товары WHERE id = ?`, id.String()).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != "10.55" {
		t.Errorf("значение изменилось: %v", got)
	}

	// #612: карта полей не должна помнить отложенное изменение как применённое.
	// Иначе следующий план сравнил бы метаданные с картой (обе — number(15,0)),
	// расхождения не увидел бы, и сужение больше никогда не предложилось, а
	// --allow-destructive его уже не догнал бы.
	changes, err := db.PlanTableChanges(ctx, metadata.TableName(after.Name), after.Fields)
	if err != nil {
		t.Fatal(err)
	}
	var reRetype *SchemaChange
	for i := range changes {
		if changes[i].Kind == ChangeRetype && changes[i].FieldID == "f_sum" {
			reRetype = &changes[i]
		}
	}
	if reRetype == nil {
		t.Fatalf("повторный план не предлагает отложенное сужение — карта записана как применённая: %+v", changes)
	}
	if !reRetype.Destructive() {
		t.Errorf("переприложенное изменение должно оставаться разрушительным: %+v", reRetype)
	}
}

// С явным разрешением сужение применяется — отказ не должен превратиться в
// невозможность изменить тип вообще.
func TestRestructureNarrowingAppliedWithPermission(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	before := catalogWithField(numField("f_sum", 15, 2))
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	after := catalogWithField(numField("f_sum", 15, 0))
	applied := 0
	db.SetSchemaOptions(SchemaOptions{AllowDestructive: true, Report: func(c SchemaChange, ok bool) {
		if ok && c.Kind == ChangeRetype {
			applied++
		}
	}})
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if applied != 1 {
		t.Fatalf("с --allow-destructive сужение обязано примениться, применено: %d", applied)
	}
}

func TestRestructureReportWaitsForOuterCommit(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)
	before := catalogWithField(metadata.Field{ID: "f_sum", Name: "Сумма", Type: metadata.FieldTypeNumber})
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	after := catalogWithField(metadata.Field{ID: "f_sum", Name: "Итого", Type: metadata.FieldTypeNumber})
	reported := 0
	db.SetSchemaOptions(SchemaOptions{Report: func(SchemaChange, bool) { reported++ }})

	sentinel := errors.New("rollback after restructure")
	earlyReports := -1
	err := db.WithTxScope(ctx, func(txCtx context.Context) error {
		if err := db.restructureTable(txCtx, metadata.TableName(after.Name), after.Fields); err != nil {
			return err
		}
		earlyReports = reported
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTxScope error = %v, want %v", err, sentinel)
	}
	if earlyReports != 0 || reported != 0 {
		t.Fatalf("Report вызван до откаченного внешнего commit: внутри=%d, после=%d", earlyReports, reported)
	}
	cols := columnNames(t, db, metadata.TableName(before.Name))
	if _, ok := cols["сумма"]; !ok {
		t.Fatalf("исходная колонка не восстановлена: %v", cols)
	}
	if _, ok := cols["итого"]; ok {
		t.Fatalf("переименование пережило rollback внешней транзакции: %v", cols)
	}
}

// Коллизия имён при удалении: поле убрали, другое поле заняло то же имя
// колонки. Add для него не создаётся, а drop по карте удалил бы колонку вместе
// с данными нового поля — и выглядело бы это штатным удалением.
func TestPlanDropCollisionRefuses(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	before := catalogWithField(metadata.Field{ID: "f_old", Name: "Телефон", Type: metadata.FieldTypeString})
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, before.Name, id, map[string]any{"Наименование": "Иванов", "Телефон": "+79161234455"}, before); err != nil {
		t.Fatal(err)
	}

	after := catalogWithField(metadata.Field{ID: "f_new", Name: "Телефон", Type: metadata.FieldTypeString})
	_, err := db.PlanTableChanges(ctx, metadata.TableName(after.Name), after.Fields)
	if err == nil {
		t.Fatal("коллизия имён обязана быть ошибкой, а не молчаливым удалением колонки")
	}
	if !strings.Contains(err.Error(), "занимает поле") {
		t.Errorf("сообщение не объясняет коллизию: %v", err)
	}

	// И миграция не должна тронуть данные.
	db.SetSchemaOptions(SchemaOptions{AllowDestructive: true})
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err == nil {
		t.Fatal("Migrate обязана отказать, пока коллизию не разобрали")
	}
	var got *string
	if err := db.QueryRow(ctx, `SELECT телефон FROM товары WHERE id = ?`, id.String()).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != "+79161234455" {
		t.Errorf("данные пострадали: %v", got)
	}
}

// Ретайп на SQLite атомарен: сорванный посреди работы, он не оставляет базу с
// данными в <колонка>__ob_retype и без самой колонки — из такого состояния
// повторный прогон не выходил вовсе.
func TestRetypeSQLiteRollsBackWhole(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	e := catalogWithField(metadata.Field{ID: "f_flag", Name: "Активен", Type: metadata.FieldTypeBool})
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}
	tbl := metadata.TableName(e.Name)

	// Занимаем имя временной колонки — на этом ADD COLUMN внутри ретайпа
	// сорвётся, то есть воспроизводим сбой посреди последовательности.
	if _, err := db.Exec(ctx, `ALTER TABLE `+quoteIdent(tbl)+` ADD COLUMN `+quoteIdent("активен__ob_retype")+` TEXT`); err != nil {
		t.Fatal(err)
	}
	err := db.applyRetype(ctx, SchemaChange{
		Table: tbl, FieldID: "f_flag", Kind: ChangeRetype, From: "bool", To: "активен",
		Field: metadata.Field{ID: "f_flag", Name: "Активен", Type: metadata.FieldTypeString},
	})
	if err == nil {
		t.Fatal("ожидалась ошибка на занятом имени временной колонки")
	}
	cols, err := db.tableColumns(ctx, tbl)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cols["активен"]; !ok {
		t.Fatalf("колонка исчезла — транзакция не откатила ретайп: %v", cols)
	}
}

// Пропавшая колонка должна называться прямо. На SQLite неизвестный идентификатор
// в двойных кавычках вырождается в строковый литерал, поэтому прежняя проверка
// возвращала «1 значение не преобразуется», а примером было имя колонки — и
// администратору предлагали исправить данные, которых нет.
func TestCheckConvertibleNamesMissingColumn(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)
	if _, err := db.Exec(ctx, `CREATE TABLE t (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO t (id) VALUES ('x')`); err != nil {
		t.Fatal(err)
	}
	bad, examples, err := db.checkConvertible(ctx, "t", "неттакойколонки",
		metadata.Field{Name: "НетТакойКолонки", Type: metadata.FieldTypeNumber})
	if err == nil {
		t.Fatalf("отсутствие колонки обязано быть ошибкой, получили bad=%d примеры=%v", bad, examples)
	}
	if !strings.Contains(err.Error(), "колонки нет") {
		t.Errorf("сообщение не про пропавшую колонку: %v", err)
	}
}
