package dbcheck

// Проверка кодов (план 117E): она отвечает на два вопроса, которые до неё
// администратор мог задать базе только руками — сколько записей без кода и
// какие коды повторяются.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func codesEnv(t *testing.T, rows []map[string]any) (*Env, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "codes.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	ent := &metadata.Entity{
		Name: "Контрагенты", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		Numerator: &metadata.Numerator{Prefix: "К-", Length: 6, Period: "none"},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	for _, r := range rows {
		if err := db.Upsert(ctx, ent.Name, uuid.New(), r, ent); err != nil {
			t.Fatalf("вставка %v: %v", r, err)
		}
	}
	return &Env{DB: db, Entities: []*metadata.Entity{ent}}, ent
}

func TestCodesCheck_FindsEmptyAndDuplicates(t *testing.T) {
	env, _ := codesEnv(t, []map[string]any{
		{"Наименование": "Без кода"},
		{metadata.StandardCodeField: "К-000001", "Наименование": "Альфа"},
		{metadata.StandardCodeField: "К-000001", "Наименование": "Бета"},
	})
	res := codesCheck{}.Run(context.Background(), env)
	if res.Severity != SeverityWarn {
		t.Fatalf("уровень = %q, ожидался warn: %+v", res.Severity, res)
	}
	var sawEmpty, sawDup bool
	for _, f := range res.Findings {
		switch {
		case strings.Contains(f.Detail, "без значения"):
			sawEmpty = true
			if f.Count != 1 {
				t.Errorf("пустых = %d, ожидалась 1", f.Count)
			}
		case strings.Contains(f.Detail, "повторяющихся"):
			sawDup = true
			if f.Count != 1 {
				t.Errorf("групп дублей = %d, ожидалась 1", f.Count)
			}
			if len(f.Examples) == 0 || !strings.Contains(f.Examples[0], "К-000001") {
				t.Errorf("в примерах нет повторяющегося кода: %v", f.Examples)
			}
		}
	}
	if !sawEmpty || !sawDup {
		t.Errorf("находки неполные: %+v", res.Findings)
	}
	if !strings.Contains(res.FixHint, "renumber") {
		t.Errorf("подсказка не ведёт к renumber: %q", res.FixHint)
	}
}

func TestCodesCheck_CleanBaseIsOK(t *testing.T) {
	env, _ := codesEnv(t, []map[string]any{
		{metadata.StandardCodeField: "К-000001", "Наименование": "Альфа"},
		{metadata.StandardCodeField: "К-000002", "Наименование": "Бета"},
	})
	res := codesCheck{}.Run(context.Background(), env)
	if res.Severity != SeverityOK {
		t.Fatalf("на чистой базе уровень = %q: %+v", res.Severity, res)
	}
}

// Объект без нумератора в проверку не попадает: у справочника без numerator:
// кода нет вовсе, и «пустых кодов столько-то» было бы шумом.
func TestCodesCheck_SkipsWithoutNumerator(t *testing.T) {
	env, ent := codesEnv(t, nil)
	ent.Numerator = nil
	res := codesCheck{}.Run(context.Background(), env)
	if res.Severity != SeverityOK || !strings.Contains(res.Summary, "автонумерацией нет") {
		t.Fatalf("справочник без нумератора попал в проверку: %+v", res)
	}
}
