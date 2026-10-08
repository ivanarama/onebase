package storage_test

// Регистронезависимый lookup предопределённых (#1622): DSL приводит имена
// свойств к нижнему регистру, поэтому storage обязан одинаково разрешать их на
// SQLite и PostgreSQL и явно отвергать case-only дубли.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func runPredefinedDSL(t *testing.T, ctx context.Context, db interpreter.PredefinedDB, expression string) (any, error) {
	t.Helper()
	src := "Функция Получить()\n  Возврат " + expression + ";\nКонецФункции"
	program, err := parser.New(lexer.New(src, "predefined.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse DSL: %v", err)
	}
	if len(program.Procedures) != 1 {
		t.Fatalf("want one DSL function, got %d", len(program.Procedures))
	}

	var result any
	err = interpreter.New().RunWithResult(
		program.Procedures[0],
		nil,
		&result,
		map[string]any{
			"ПредопределённыеЗначения": interpreter.NewPredefinedRoot(ctx, db),
		},
	)
	return result, err
}

func TestGetPredefinedIDCaseFoldAndResyncMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		catalog := &metadata.Entity{
			Name:   "Валюты",
			Kind:   metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			Predefined: []*metadata.PredefinedItem{{
				Name: "Рубль", Fields: map[string]any{"Наименование": "Российский рубль"},
			}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{catalog}); err != nil {
			t.Fatal(err)
		}

		exact, err := db.GetPredefinedID(ctx, catalog.Name, "Рубль")
		if err != nil {
			t.Fatalf("точное имя не нашлось: %v", err)
		}
		got, err := runPredefinedDSL(t, ctx, db, "ПредопределённыеЗначения.ВАЛЮТЫ.РУБЛЬ")
		if err != nil {
			t.Fatalf("публичный DSL lookup: %v", err)
		}
		if got != exact.String() {
			t.Fatalf("DSL дал другой id: %v != %s", got, exact)
		}

		// Case-only rename в конфигурации не должен менять UUID или создавать
		// вторую строку с тем же логическим именем.
		catalog.Predefined[0].Name = "рубль"
		if err := db.SyncPredefined(ctx, catalog); err != nil {
			t.Fatalf("case-only resync: %v", err)
		}
		resynced, err := db.GetPredefinedID(ctx, catalog.Name, "РУБЛЬ")
		if err != nil {
			t.Fatalf("lookup после resync: %v", err)
		}
		if resynced != exact {
			t.Fatalf("case-only resync изменил UUID: %s != %s", resynced, exact)
		}
		var count int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+metadata.TableName(catalog.Name)).Scan(&count); err != nil {
			t.Fatalf("count predefined rows: %v", err)
		}
		if count != 1 {
			t.Fatalf("case-only resync создал дубль: строк %d, ожидалась 1", count)
		}
	})
}

func TestPredefinedCaseFoldAmbiguityThroughDSLMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		catalog := &metadata.Entity{
			Name:   "Валюты",
			Kind:   metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			Predefined: []*metadata.PredefinedItem{
				{Name: "Рубль", Fields: map[string]any{"Наименование": "РФ"}},
				{Name: "Доллар", Fields: map[string]any{"Наименование": "США"}},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{catalog}); err != nil {
			t.Fatal(err)
		}

		// Имитируем достижимое legacy-состояние: exact "рубль" существует рядом
		// с "Рубль". Старый fast-path молча выбирал exact-строку после того, как
		// интерпретатор приводил поле к нижнему регистру.
		d := db.Dialect()
		_, err := db.Exec(ctx,
			"UPDATE "+metadata.TableName(catalog.Name)+" SET _predefined_name = "+d.Placeholder(1)+" WHERE _predefined_name = "+d.Placeholder(2),
			"рубль", "Доллар",
		)
		if err != nil {
			t.Fatalf("prepare case-only duplicate: %v", err)
		}

		_, err = runPredefinedDSL(t, ctx, db, "ПредопределённыеЗначения.Валюты.РуБлЬ")
		if err == nil {
			t.Fatal("неоднозначное имя должно давать ошибку")
		}
		var dslErr *interpreter.DSLError
		if !errors.As(err, &dslErr) {
			t.Fatalf("want DSLError, got %T: %v", err, err)
		}
		if !strings.Contains(strings.ToLower(dslErr.Msg), "неоднознач") {
			t.Fatalf("ожидалась неоднозначность, получили: %v", err)
		}
		if strings.Contains(strings.ToLower(dslErr.Msg), "не найден") {
			t.Fatalf("proxy замаскировал неоднозначность как отсутствие: %v", err)
		}
	})
}

type predefinedDBFunc func(context.Context, string, string) (string, error)

func (f predefinedDBFunc) GetPredefinedIDStr(ctx context.Context, entityName, itemName string) (string, error) {
	return f(ctx, entityName, itemName)
}

func TestPredefinedProxyPreservesOperationalError(t *testing.T) {
	operational := errors.New("database unavailable")
	db := predefinedDBFunc(func(context.Context, string, string) (string, error) {
		return "", operational
	})

	_, err := runPredefinedDSL(t, context.Background(), db, "ПредопределённыеЗначения.Валюты.Рубль")
	if err == nil {
		t.Fatal("operational error must reach the DSL caller")
	}
	var dslErr *interpreter.DSLError
	if !errors.As(err, &dslErr) {
		t.Fatalf("want DSLError, got %T: %v", err, err)
	}
	if !errors.Is(err, operational) {
		t.Fatalf("operational cause was lost: %v", err)
	}
	if !strings.Contains(dslErr.Msg, operational.Error()) {
		t.Fatalf("operational detail was hidden: %v", err)
	}
	if strings.Contains(strings.ToLower(dslErr.Msg), "не найден") {
		t.Fatalf("operational error was masked as not found: %v", err)
	}
}
