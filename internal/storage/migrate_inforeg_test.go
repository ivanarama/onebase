package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// period): MigrateInfoRegisters должна
// дотягивать схему существующих таблиц. Если YAML обновился — добавлены
// колонки, periodic-флаг — миграция должна добавить недостающие колонки
// без потери данных.
func TestMigrateInfoRegisters_AddsPeriodColumn(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// 1. Создаём регистр БЕЗ periodic — таблица без period.
	v1 := &metadata.InfoRegister{
		Name:     "ЦеныНоменклатуры",
		Periodic: false,
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", Type: "string"},
		},
		Resources: []metadata.Field{
			{Name: "Цена", Type: "number"},
		},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{v1}); err != nil {
		t.Fatalf("v1 migrate: %v", err)
	}
	// Проверим — period не должно быть.
	if has, _ := db.dialect.ColumnExists(ctx, db, "инфо_ценыноменклатуры", "period"); has {
		t.Fatal("на v1 не должно быть period")
	}

	// 2. Обновляем — periodic: true. Старая таблица существует. Миграция
	//    должна добавить period.
	v2 := &metadata.InfoRegister{
		Name:     "ЦеныНоменклатуры",
		Periodic: true,
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", Type: "string"},
		},
		Resources: []metadata.Field{
			{Name: "Цена", Type: "number"},
		},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{v2}); err != nil {
		t.Fatalf("v2 migrate: %v", err)
	}
	if has, err := db.dialect.ColumnExists(ctx, db, "инфо_ценыноменклатуры", "period"); err != nil {
		t.Fatalf("проверка period: %v", err)
	} else if !has {
		t.Error("после v2 миграции должна появиться колонка period")
	}
}

// Добавление новых измерений в существующий info-регистр.
func TestMigrateInfoRegisters_AddsDimension(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	v1 := &metadata.InfoRegister{
		Name:     "Курсы",
		Periodic: true,
		Dimensions: []metadata.Field{
			{Name: "Валюта", Type: "string"},
		},
		Resources: []metadata.Field{
			{Name: "Курс", Type: "number"},
		},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{v1}); err != nil {
		t.Fatal(err)
	}

	v2 := &metadata.InfoRegister{
		Name:     "Курсы",
		Periodic: true,
		Dimensions: []metadata.Field{
			{Name: "Валюта", Type: "string"},
			{Name: "Биржа", Type: "string"}, // новое измерение
		},
		Resources: []metadata.Field{
			{Name: "Курс", Type: "number"},
		},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{v2}); err != nil {
		t.Fatalf("v2 migrate: %v", err)
	}
	if has, _ := db.dialect.ColumnExists(ctx, db, "инфо_курсы", "биржа"); !has {
		t.Error("новое измерение «Биржа» не добавлено")
	}
}

// при mismatch фактического PK (наследие старого CREATE)
// миграция пересоздаёт таблицу с правильным PK через CREATE+INSERT SELECT+DROP+RENAME.
func TestMigrateInfoRegisters_RebuildsPK(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Создаём «старую» неправильную таблицу — PK только по номенклатура_id.
	_, err = db.Exec(ctx, `CREATE TABLE инфо_ценыноменклатуры (
		номенклатура_id TEXT NOT NULL,
		цена TEXT,
		PRIMARY KEY (номенклатура_id)
	)`)
	if err != nil {
		t.Fatal(err)
	}

	ir := &metadata.InfoRegister{
		Name:     "ЦеныНоменклатуры",
		Periodic: true,
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", Type: "reference:Номенклатура", RefEntity: "Номенклатура"},
			{Name: "ТипЦен", Type: "reference:ТипЦен", RefEntity: "ТипЦен"},
		},
		Resources: []metadata.Field{
			{Name: "Цена", Type: "number"},
		},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Проверим что PK теперь (period, номенклатура_id, типцен_id).
	rows, err := db.Query(ctx, `PRAGMA table_info("инфо_ценыноменклатуры")`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var pks []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if pk > 0 {
			pks = append(pks, name)
		}
	}
	if len(pks) != 3 {
		t.Errorf("ожидалось 3 колонки в PK, получили %v", pks)
	}
}

// #616: удаление измерения регистра сведений без --allow-destructive НЕ должно
// терять данные. Измерение входит в PK, поэтому его удаление даёт mismatch и шло
// в rebuild fixInfoRegPKSQLite, который пересоздавал таблицу по новым метаданным
// и терял осиротевшую колонку — в обход отказа плана 81.
func TestMigrateInfoRegisters_DropDimensionKeepsDataWithoutPermission(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	v1 := &metadata.InfoRegister{
		Name: "ЦеныНоменклатуры",
		Dimensions: []metadata.Field{
			{ID: "d_nom", Name: "Номенклатура", Type: "string"},
			{ID: "d_sklad", Name: "Склад", Type: "string"},
		},
		Resources: []metadata.Field{{ID: "r_price", Name: "Цена", Type: "number"}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{v1}); err != nil {
		t.Fatalf("v1 migrate: %v", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO инфо_ценыноменклатуры (номенклатура, склад, цена) VALUES ('Гвозди','Основной','10')`); err != nil {
		t.Fatal(err)
	}

	// Убираем измерение «Склад» — без --allow-destructive (schemaOpts по умолчанию).
	v2 := &metadata.InfoRegister{
		Name:       "ЦеныНоменклатуры",
		Dimensions: []metadata.Field{{ID: "d_nom", Name: "Номенклатура", Type: "string"}},
		Resources:  []metadata.Field{{ID: "r_price", Name: "Цена", Type: "number"}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{v2}); err != nil {
		t.Fatalf("v2 migrate: %v", err)
	}

	// Колонка «склад» и данные на месте: разрушительное изменение отложено.
	if has, _ := db.dialect.ColumnExists(ctx, db, "инфо_ценыноменклатуры", "склад"); !has {
		t.Fatal("колонка «склад» удалена без --allow-destructive — данные потеряны")
	}
	var sklad, price string
	if err := db.QueryRow(ctx,
		`SELECT склад, CAST(цена AS TEXT) FROM инфо_ценыноменклатуры WHERE номенклатура='Гвозди'`).Scan(&sklad, &price); err != nil {
		t.Fatalf("строка исчезла: %v", err)
	}
	if sklad != "Основной" || price != "10" {
		t.Errorf("данные искажены: склад=%q цена=%q", sklad, price)
	}
}

// А с --allow-destructive удаление измерения применяется осознанно: колонка и её
// данные исчезают, PK перестраивается на оставшееся измерение.
func TestMigrateInfoRegisters_DropDimensionLosesDataWithPermission(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	v1 := &metadata.InfoRegister{
		Name: "ЦеныНоменклатуры",
		Dimensions: []metadata.Field{
			{ID: "d_nom", Name: "Номенклатура", Type: "string"},
			{ID: "d_sklad", Name: "Склад", Type: "string"},
		},
		Resources: []metadata.Field{{ID: "r_price", Name: "Цена", Type: "number"}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{v1}); err != nil {
		t.Fatalf("v1 migrate: %v", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO инфо_ценыноменклатуры (номенклатура, склад, цена) VALUES ('Гвозди','Основной','10')`); err != nil {
		t.Fatal(err)
	}

	db.SetSchemaOptions(SchemaOptions{AllowDestructive: true})
	v2 := &metadata.InfoRegister{
		Name:       "ЦеныНоменклатуры",
		Dimensions: []metadata.Field{{ID: "d_nom", Name: "Номенклатура", Type: "string"}},
		Resources:  []metadata.Field{{ID: "r_price", Name: "Цена", Type: "number"}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{v2}); err != nil {
		t.Fatalf("v2 migrate: %v", err)
	}

	if has, _ := db.dialect.ColumnExists(ctx, db, "инфо_ценыноменклатуры", "склад"); has {
		t.Fatal("с --allow-destructive колонка «склад» должна исчезнуть")
	}
	// Остальные данные сохранены, PK перестроен на оставшееся измерение.
	var price string
	if err := db.QueryRow(ctx,
		`SELECT CAST(цена AS TEXT) FROM инфо_ценыноменклатуры WHERE номенклатура='Гвозди'`).Scan(&price); err != nil {
		t.Fatalf("строка исчезла целиком: %v", err)
	}
	if price != "10" {
		t.Errorf("цена искажена: %q", price)
	}
}

// Добавление новых ресурсов уже работало — регресс-тест на это.
func TestMigrateInfoRegisters_AddsResource(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	v1 := &metadata.InfoRegister{
		Name:     "Курсы",
		Periodic: true,
		Dimensions: []metadata.Field{
			{Name: "Валюта", Type: "string"},
		},
		Resources: []metadata.Field{
			{Name: "Курс", Type: "number"},
		},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{v1}); err != nil {
		t.Fatal(err)
	}

	v2 := &metadata.InfoRegister{
		Name:     "Курсы",
		Periodic: true,
		Dimensions: []metadata.Field{
			{Name: "Валюта", Type: "string"},
		},
		Resources: []metadata.Field{
			{Name: "Курс", Type: "number"},
			{Name: "Кратность", Type: "number"}, // новый ресурс
		},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{v2}); err != nil {
		t.Fatalf("v2 migrate: %v", err)
	}
	if has, _ := db.dialect.ColumnExists(ctx, db, "инфо_курсы", "кратность"); !has {
		t.Error("новый ресурс «Кратность» не добавлен")
	}
}
