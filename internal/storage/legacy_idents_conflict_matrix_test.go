package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Решение владельца по #1946: неоднозначное прежнее имя — отказ, а не
// угадывание. Миграция не переименовывает ничего, чью принадлежность или
// сохранность данных нельзя доказать, а `migrate --dry-run` показывает это
// заранее (PlanLongIdents).

// Два реквизита с общими первыми 31 буквами PostgreSQL обрезал в одно имя.
// Колонка под таким именем — чьё в ней значение, не установить.
func TestMigrateRefusesAmbiguousLegacyColumn_Matrix(t *testing.T) {
	cat := &metadata.Entity{
		Name: "ФилиалКонфликт",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "ИспользоватьКоэффициентИзмененияЦенМастера", Type: metadata.FieldTypeString},
			{Name: "ИспользоватьКоэффициентИзмененияЦенВРемонт", Type: metadata.FieldTypeString},
		},
	}
	a, b := metadata.LogicalColumnName(cat.Fields[0]), metadata.LogicalColumnName(cat.Fields[1])
	truncated := metadata.LegacySQLIdents(a)[1]
	if truncated != metadata.LegacySQLIdents(b)[1] {
		t.Fatal("реквизиты должны обрезаться PostgreSQL в одно имя — иначе тест ничего не проверяет")
	}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		d := db.Dialect()
		table := metadata.TableName(cat.Name)
		if _, err := db.Exec(ctx, "CREATE TABLE "+table+" (id "+d.TypeUUID()+" PRIMARY KEY, "+truncated+" TEXT)"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, "INSERT INTO "+table+" (id, "+truncated+") VALUES ("+d.Placeholder(1)+", 'да')", uuid.New().String()); err != nil {
			t.Fatal(err)
		}
		plan, err := db.PlanLongIdents(ctx, []*metadata.Entity{cat}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan) != 2 || plan[0].Conflict == "" || plan[1].Conflict == "" {
			t.Fatalf("dry-run обязан показать конфликт по обоим реквизитам: %v", plan)
		}
		err = db.Migrate(ctx, []*metadata.Entity{cat})
		if err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") || !strings.Contains(err.Error(), truncated) {
			t.Fatalf("миграция обязана отказать с конфликтом по %s, получили: %v", truncated, err)
		}
		// Ничего не переименовано: прежняя колонка с данными на месте.
		var v string
		if err := db.QueryRow(ctx, "SELECT "+truncated+" FROM "+table).Scan(&v); err != nil || v != "да" {
			t.Fatalf("прежняя колонка тронута: %q, %v", v, err)
		}
	})
}

// И новая колонка, и прежняя с данными — какие данные верные, не установить:
// отказ. Прежняя без данных — переносить нечего, миграция проходит.
func TestMigrateRefusesLegacyColumnBesideNew_Matrix(t *testing.T) {
	cat := &metadata.Entity{
		Name:   "КонфликтНовойИПрежней",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "РеквизитСОченьДлиннымИменемКоторыйПришёлИзОдинЭс", Type: metadata.FieldTypeString}},
	}
	logical := metadata.LogicalColumnName(cat.Fields[0])
	full, target := metadata.LegacySQLIdents(logical)[0], metadata.SQLIdent(logical)
	for _, withData := range []bool{true, false} {
		dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
			ctx := context.Background()
			d := db.Dialect()
			table := metadata.TableName(cat.Name)
			legacy := full
			if !db.IsSQLite() {
				legacy = metadata.LegacySQLIdents(logical)[1] // так её хранил PostgreSQL
			}
			if _, err := db.Exec(ctx, "CREATE TABLE "+table+" (id "+d.TypeUUID()+" PRIMARY KEY, "+target+" TEXT, "+legacy+" TEXT)"); err != nil {
				t.Fatal(err)
			}
			if withData {
				if _, err := db.Exec(ctx, "INSERT INTO "+table+" (id, "+legacy+") VALUES ("+d.Placeholder(1)+", 'старое')", uuid.New().String()); err != nil {
					t.Fatal(err)
				}
			}
			err := db.Migrate(ctx, []*metadata.Entity{cat})
			if withData && (err == nil || !strings.Contains(err.Error(), "есть и новая колонка")) {
				t.Fatalf("с данными в прежней колонке — отказ, получили: %v", err)
			}
			if !withData && err != nil {
				t.Fatalf("прежняя колонка пуста — миграция проходит, получили: %v", err)
			}
		})
	}
}

// Таблица сущности и её табличная часть с общим началом: PostgreSQL обрезал
// их в одно имя. Чья таблица под этим именем — не установить: отказ.
func TestMigrateRefusesAmbiguousLegacyTable_Matrix(t *testing.T) {
	doc := &metadata.Entity{
		Name:   "ДокументСОченьДлиннымИменемИзКонфигурацииОдинЭс",
		Kind:   metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{{Name: "Строчка", Type: metadata.FieldTypeString}}},
		},
	}
	tableOld := metadata.LegacySQLIdents(metadata.TableLogical(doc.Name))
	tpOld := metadata.LegacySQLIdents(metadata.TablePartTableLogical(doc.Name, "Товары"))
	if tableOld == nil || tpOld == nil || tableOld[1] != tpOld[1] {
		t.Fatal("таблица и табличная часть должны обрезаться в одно имя — иначе тест ничего не проверяет")
	}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if _, err := db.Exec(ctx, "CREATE TABLE "+tableOld[1]+" (id "+db.Dialect().TypeUUID()+" PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
		err := db.Migrate(ctx, []*metadata.Entity{doc})
		if err == nil || !strings.Contains(err.Error(), "претендуют 2 таблицы") {
			t.Fatalf("общее прежнее имя таблицы и табличной части — отказ, получили: %v", err)
		}
	})
}

// dry-run показывает переименование до миграции, сама база не меняется.
func TestPlanLongIdentsListsRename_Matrix(t *testing.T) {
	cat := &metadata.Entity{
		Name:   "СправочникСОченьДлиннымИменемИзКонфигурации1С",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	logical := metadata.TableLogical(cat.Name)
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		old := metadata.LegacySQLIdents(logical)[0]
		if !db.IsSQLite() {
			old = metadata.LegacySQLIdents(logical)[1]
		}
		if _, err := db.Exec(ctx, "CREATE TABLE "+old+" (id "+db.Dialect().TypeUUID()+" PRIMARY KEY, наименование TEXT)"); err != nil {
			t.Fatal(err)
		}
		plan, err := db.PlanLongIdents(ctx, []*metadata.Entity{cat}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan) != 1 || plan[0].Old != old || plan[0].New != metadata.SQLIdent(logical) || plan[0].Conflict != "" {
			t.Fatalf("ждали одно переименование %s → %s, получили %v", old, metadata.SQLIdent(logical), plan)
		}
		if exists, err := db.TableExists(ctx, old); err != nil || !exists {
			t.Fatalf("dry-run не должен менять базу: %v", err)
		}
	})
}

// Короткое действующее имя совпадает с прежней обрезкой нового длинного:
// его данные нельзя присвоить новому владельцу даже при одном длинном имени.
func TestMigrateKeepsCurrentShortPrefix_Matrix(t *testing.T) {
	short := strings.Repeat("Я", 31)
	for _, kind := range []string{"column", "table"} {
		t.Run(kind, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				id := uuid.New()
				owner := &metadata.Entity{Name: "Владелец", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: short, Type: metadata.FieldTypeString}}}
				if kind == "table" {
					owner.Name = short
					owner.Fields = []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}
				}
				if err := db.Migrate(ctx, []*metadata.Entity{owner}); err != nil {
					t.Fatal(err)
				}
				field := owner.Fields[0].Name
				if err := db.Upsert(ctx, owner.Name, id, map[string]any{field: "исходные данные"}, owner); err != nil {
					t.Fatal(err)
				}
				original := *owner
				entities := []*metadata.Entity{owner}
				if kind == "column" {
					owner.Fields = append(owner.Fields, metadata.Field{Name: short + "Продолжение", Type: metadata.FieldTypeString})
				} else {
					entities = append(entities, &metadata.Entity{Name: short + "Продолжение", Kind: metadata.KindCatalog})
				}
				plan, err := db.PlanLongIdents(ctx, entities, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(plan) == 0 || plan[0].Conflict == "" {
					t.Fatalf("не обнаружен занятый текущий идентификатор: %v", plan)
				}
				err = db.Migrate(ctx, entities)
				if err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") {
					t.Fatalf("нужен отказ до ALTER: %v", err)
				}
				row, err := db.GetByID(ctx, owner.Name, id, &original)
				if err != nil || row[field] != "исходные данные" {
					t.Fatalf("данные действующего имени потеряны: %v %v", row, err)
				}
			})
		})
	}
}

func TestPlanLongIdentsIncludesAccountsAndTotals_Matrix(t *testing.T) {
	resource := metadata.Field{Name: "Сумма", Type: metadata.FieldTypeNumber}
	reg := &metadata.Register{Name: "РегистрНакопленияСОченьДлиннымИменемИзКонфигурации", Resources: []metadata.Field{resource}, Totals: metadata.RegisterTotals{Enabled: true}}
	ar := &metadata.AccountRegister{Name: "БухгалтерскийРегистрСОченьДлиннымИменемИзКонфигурации", Resources: []metadata.Field{resource}, Totals: metadata.RegisterTotals{Enabled: true}}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
			t.Fatal(err)
		}
		tables := []string{metadata.RegisterTableLogical(reg.Name), metadata.RegisterTotalsTableLogical(reg.Name), metadata.AccountRegTableLogical(ar.Name), metadata.AccountRegTotalsTableLogical(ar.Name)}
		for _, table := range tables {
			old := metadata.LegacySQLIdents(table)[0]
			if !db.IsSQLite() {
				old = metadata.LegacySQLIdents(table)[1]
			}
			if _, err := db.Exec(ctx, "ALTER TABLE "+metadata.SQLIdent(table)+" RENAME TO "+old); err != nil {
				t.Fatal(err)
			}
		}
		plan, err := db.PlanLongIdents(ctx, nil, []*metadata.Register{reg}, nil, []*metadata.AccountRegister{ar})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan) != 4 {
			t.Fatalf("план должен включать все четыре таблицы: %v", plan)
		}
		for _, table := range tables {
			found := false
			for _, a := range plan {
				if a.New == metadata.SQLIdent(table) && a.Conflict == "" {
					found = true
				}
			}
			if !found {
				t.Fatalf("нет таблицы %s: %v", table, plan)
			}
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
			t.Fatal(err)
		}
		plan, err = db.PlanLongIdents(ctx, nil, []*metadata.Register{reg}, nil, []*metadata.AccountRegister{ar})
		if err != nil || len(plan) != 0 {
			t.Fatalf("после миграции план не пуст: %v %v", plan, err)
		}
	})
}

func TestPlanLongIdentsIncludesTotalsColumnConflicts_Matrix(t *testing.T) {
	long := strings.Repeat("Я", 31) + "Продолжение"
	reg := &metadata.Register{Name: "Регистр", Resources: []metadata.Field{{Name: long, Type: metadata.FieldTypeNumber}}, Totals: metadata.RegisterTotals{Enabled: true}}
	ar := &metadata.AccountRegister{Name: "Бухгалтерский", Resources: []metadata.Field{{Name: long, Type: metadata.FieldTypeNumber}}, Totals: metadata.RegisterTotals{Enabled: true}}
	for _, kind := range []string{"register", "account"} {
		t.Run(kind, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				table := metadata.RegisterTotalsTableName(reg.Name)
				logical := metadata.LogicalColumnName(reg.Resources[0])
				var regs []*metadata.Register
				var accounts []*metadata.AccountRegister
				regs = []*metadata.Register{reg}
				if kind == "account" {
					table = metadata.AccountRegTotalsTableName(ar.Name)
					logical += "_дт"
					regs = nil
					accounts = []*metadata.AccountRegister{ar}
				}
				old := metadata.LegacySQLIdents(logical)[1]
				target := metadata.SQLIdent(logical)
				if _, err := db.Exec(ctx, "CREATE TABLE "+table+" ("+old+" TEXT, "+target+" TEXT)"); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(ctx, "INSERT INTO "+table+" ("+old+") VALUES ('прежние данные')"); err != nil {
					t.Fatal(err)
				}
				plan, err := db.PlanLongIdents(ctx, nil, regs, nil, accounts)
				if err != nil {
					t.Fatal(err)
				}
				if len(plan) == 0 || plan[0].Conflict == "" || plan[0].Table != table {
					t.Fatalf("конфликт колонок итогов не попал в план: %v", plan)
				}
				if kind == "account" {
					err = db.MigrateAccountRegisters(ctx, accounts)
				} else {
					err = db.MigrateRegisters(ctx, regs)
				}
				if err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") {
					t.Fatalf("миграция должна остановиться до таблицы движений: %v", err)
				}
				var value string
				if err := db.QueryRow(ctx, "SELECT "+old+" FROM "+table).Scan(&value); err != nil || value != "прежние данные" {
					t.Fatalf("конфликтующая колонка изменена: %q %v", value, err)
				}
			})
		})
	}
}
