package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func schemaTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "schema.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func catalogWithField(f metadata.Field) *metadata.Entity {
	return &metadata.Entity{
		Name: "Товары",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
			f,
		},
	}
}

func columnNames(t *testing.T, db *DB, table string) map[string]string {
	t.Helper()
	cols, err := db.tableColumns(context.Background(), table)
	if err != nil {
		t.Fatal(err)
	}
	return cols
}

// Главный регресс плана 81: переименование реквизита сохраняет данные.
//
// До плана поле «Сумма» → «СуммаДокумента» выглядело для миграции как
// «одно поле исчезло, другое появилось»: заводилась новая ПУСТАЯ колонка, а
// накопленные суммы оставались в осиротевшей старой.
func TestRestructureRenameKeepsData(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	before := catalogWithField(metadata.Field{ID: "f_sum", Name: "Сумма", Type: metadata.FieldTypeNumber})
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, before.Name, id, map[string]any{"Наименование": "Гвозди", "Сумма": "150.50"}, before); err != nil {
		t.Fatal(err)
	}

	// Переименовали реквизит в конфигураторе: имя другое, id тот же.
	after := catalogWithField(metadata.Field{ID: "f_sum", Name: "СуммаДокумента", Type: metadata.FieldTypeNumber})
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err != nil {
		t.Fatal(err)
	}

	cols := columnNames(t, db, "товары")
	if _, ok := cols["суммадокумента"]; !ok {
		t.Fatalf("колонка не переименована, есть только: %v", cols)
	}
	if _, ok := cols["сумма"]; ok {
		t.Fatalf("старая колонка осталась осиротевшей: %v", cols)
	}

	var got string
	if err := db.QueryRow(ctx, `SELECT суммадокумента FROM товары WHERE id = ?`, id.String()).Scan(&got); err != nil {
		t.Fatalf("чтение значения: %v", err)
	}
	if !strings.HasPrefix(got, "150.5") {
		t.Fatalf("данные потеряны при переименовании: %q", got)
	}
}

// Поле без id мигрирует как раньше — аддитивно. Это обратная совместимость:
// существующие конфигурации не обязаны знать про id, но и переименование им
// по-прежнему не даётся.
func TestRestructureWithoutIDStaysAdditive(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	before := &metadata.Entity{Name: "Товары", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Сумма", Type: metadata.FieldTypeNumber},
	}}
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	after := &metadata.Entity{Name: "Товары", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "СуммаДокумента", Type: metadata.FieldTypeNumber},
	}}
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err != nil {
		t.Fatal(err)
	}

	cols := columnNames(t, db, "товары")
	if _, ok := cols["сумма"]; !ok {
		t.Error("без id старая колонка должна остаться (прежнее поведение)")
	}
	if _, ok := cols["суммадокумента"]; !ok {
		t.Error("без id новая колонка должна добавиться (прежнее поведение)")
	}
}

// Смена типа отменяется ДО потери данных: значения, которые не станут значением
// нового типа, ищутся заранее.
func TestRestructureRetypeRefusesUnconvertible(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	// Строка → булево меняет хранение и на SQLite (TEXT → INTEGER).
	before := catalogWithField(metadata.Field{ID: "f_flag", Name: "Признак", Type: metadata.FieldTypeString})
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, before.Name, id, map[string]any{"Наименование": "Гвозди", "Признак": "не разобрать"}, before); err != nil {
		t.Fatal(err)
	}

	after := catalogWithField(metadata.Field{ID: "f_flag", Name: "Признак", Type: metadata.FieldTypeBool})
	err := db.Migrate(ctx, []*metadata.Entity{after})
	if err == nil {
		t.Fatal("миграция обязана отказаться: значение не преобразуется")
	}
	for _, want := range []string{"не преобразуются", "не разобрать"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в сообщении нет %q: %v", want, err)
		}
	}

	// Данные и колонка не тронуты.
	var got string
	if err := db.QueryRow(ctx, `SELECT признак FROM товары WHERE id = ?`, id.String()).Scan(&got); err != nil {
		t.Fatalf("чтение значения: %v", err)
	}
	if got != "не разобрать" {
		t.Fatalf("значение изменилось при отказе: %q", got)
	}
}

func TestRestructureRetypeConverts(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	before := catalogWithField(metadata.Field{ID: "f_flag", Name: "Признак", Type: metadata.FieldTypeString})
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, before.Name, id, map[string]any{"Наименование": "Гвозди", "Признак": "1"}, before); err != nil {
		t.Fatal(err)
	}

	after := catalogWithField(metadata.Field{ID: "f_flag", Name: "Признак", Type: metadata.FieldTypeBool})
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err != nil {
		t.Fatalf("миграция с преобразуемыми значениями: %v", err)
	}
	cols := columnNames(t, db, "товары")
	if got := strings.ToUpper(cols["признак"]); got != "INTEGER" {
		t.Fatalf("тип колонки не изменён: %q", got)
	}
	var got int
	if err := db.QueryRow(ctx, `SELECT признак FROM товары WHERE id = ?`, id.String()).Scan(&got); err != nil {
		t.Fatalf("чтение значения: %v", err)
	}
	if got != 1 {
		t.Fatalf("значение не перенесено: %d", got)
	}
}

// Удаление поля из конфигурации не удаляет колонку без явного разрешения.
func TestRestructureDropRequiresPermission(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	before := catalogWithField(metadata.Field{ID: "f_note", Name: "Комментарий", Type: metadata.FieldTypeString})
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	after := &metadata.Entity{Name: "Товары", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
	}}

	var reported []SchemaChange
	var appliedFlags []bool
	db.SetSchemaOptions(SchemaOptions{Report: func(c SchemaChange, applied bool) {
		reported = append(reported, c)
		appliedFlags = append(appliedFlags, applied)
	}})
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err != nil {
		t.Fatal(err)
	}
	if _, ok := columnNames(t, db, "товары")["комментарий"]; !ok {
		t.Fatal("без разрешения колонка удаляться не должна")
	}
	if len(reported) != 1 || reported[0].Kind != ChangeDrop || appliedFlags[0] {
		t.Fatalf("ожидался неприменённый drop, получено %+v (applied=%v)", reported, appliedFlags)
	}

	// С явным разрешением — удаляем.
	db.SetSchemaOptions(SchemaOptions{AllowDestructive: true})
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err != nil {
		t.Fatal(err)
	}
	if _, ok := columnNames(t, db, "товары")["комментарий"]; ok {
		t.Fatal("с разрешением колонка должна быть удалена")
	}
}

// Пробный прогон показывает план и не трогает базу.
func TestPlanMigrationChangesNothing(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	before := catalogWithField(metadata.Field{ID: "f_sum", Name: "Сумма", Type: metadata.FieldTypeNumber})
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}

	after := catalogWithField(metadata.Field{ID: "f_sum", Name: "СуммаДокумента", Type: metadata.FieldTypeNumber})
	plan, err := db.PlanMigration(ctx, []*metadata.Entity{after}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Kind != ChangeRename {
		t.Fatalf("ожидалось одно переименование в плане, получено %+v", plan)
	}
	if !strings.Contains(plan[0].String(), "данные сохраняются") {
		t.Errorf("строка плана не объясняет, что будет с данными: %s", plan[0])
	}

	cols := columnNames(t, db, "товары")
	if _, ok := cols["сумма"]; !ok {
		t.Error("построение плана переименовало колонку")
	}
	if _, ok := cols["суммадокумента"]; ok {
		t.Error("построение плана завело новую колонку")
	}
}

// План по несуществующей таблице пуст: её создаст обычная миграция.
func TestPlanMigrationSkipsMissingTables(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	e := catalogWithField(metadata.Field{ID: "f_sum", Name: "Сумма", Type: metadata.FieldTypeNumber})
	plan, err := db.PlanMigration(ctx, []*metadata.Entity{e}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 0 {
		t.Fatalf("для новой базы план должен быть пуст, получено %+v", plan)
	}
}

// Табличные части реструктурируются так же, как шапка.
func TestRestructureTablePartRename(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	mk := func(fieldName string) *metadata.Entity {
		return &metadata.Entity{
			Name: "Реализация", Kind: metadata.KindDocument,
			Fields: []metadata.Field{{ID: "f_num", Name: "Номер", Type: metadata.FieldTypeString}},
			TableParts: []metadata.TablePart{{
				Name:   "Товары",
				Fields: []metadata.Field{{ID: "tp_price", Name: fieldName, Type: metadata.FieldTypeNumber}},
			}},
		}
	}
	if err := db.Migrate(ctx, []*metadata.Entity{mk("Цена")}); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, []*metadata.Entity{mk("ЦенаЗаЕдиницу")}); err != nil {
		t.Fatal(err)
	}
	cols := columnNames(t, db, metadata.TablePartTableName("Реализация", "Товары"))
	if _, ok := cols["ценазаединицу"]; !ok {
		t.Fatalf("колонка ТЧ не переименована: %v", cols)
	}
	if _, ok := cols["цена"]; ok {
		t.Fatalf("старая колонка ТЧ осталась: %v", cols)
	}
}

// Повторная миграция без изменений не должна ничего планировать — иначе
// каждый запуск сервера гонял бы DDL по живой базе.
func TestRestructureIdempotent(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	e := catalogWithField(metadata.Field{ID: "f_sum", Name: "Сумма", Type: metadata.FieldTypeNumber})
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}
	var plan []SchemaChange
	db.SetSchemaOptions(SchemaOptions{Report: func(c SchemaChange, _ bool) { plan = append(plan, c) }})
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}
	if len(plan) != 0 {
		t.Fatalf("повторная миграция запланировала изменения: %+v", plan)
	}
}
