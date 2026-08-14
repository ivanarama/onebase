package cli

// onebase renumber — дозаполнение пустых кодов и номеров (план 117C).
//
// Команда нужна потому, что при включении нумератора на живой базе старые
// элементы остаются без кода: автоматически проставлять его при правке YAML
// нельзя — правка конфигурации не должна незаметно переписывать данные.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

func renumberCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name: "Контрагенты",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		Numerator: &metadata.Numerator{Prefix: "К-", Length: 6, Period: "none"},
	}
}

func renumberDB(t *testing.T, ent *metadata.Entity, rows []map[string]any) *storage.DB {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "renumber.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	if err := db.EnsureNumeratorSchema(ctx); err != nil {
		t.Fatalf("схема нумератора: %v", err)
	}
	for _, r := range rows {
		if err := db.Upsert(ctx, ent.Name, uuid.New(), r, ent); err != nil {
			t.Fatalf("вставка %v: %v", r, err)
		}
	}
	return db
}

func codesOf(t *testing.T, db *storage.DB, ent *metadata.Entity) map[string]string {
	t.Helper()
	rows, err := db.List(context.Background(), ent.Name, ent, storage.ListParams{Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	out := map[string]string{}
	for _, r := range rows {
		name := strings.TrimSpace(asAnyString(rowFieldValue(r, "Наименование")))
		out[name] = strings.TrimSpace(asAnyString(rowFieldValue(r, metadata.StandardCodeField)))
	}
	return out
}

func asAnyString(v any) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// Без --write команда ничего не пишет: администратор сначала видит объём.
func TestRenumber_DryRunWritesNothing(t *testing.T) {
	ent := renumberCatalog()
	db := renumberDB(t, ent, []map[string]any{
		{"Наименование": "Альфа"},
		{"Наименование": "Бета"},
	})

	rep, err := renumberEntity(context.Background(), db, ent, false)
	if err != nil {
		t.Fatalf("renumberEntity: %v", err)
	}
	if rep.Empty != 2 || rep.Filled != 0 {
		t.Errorf("отчёт = %+v, ожидалось empty=2 filled=0", rep)
	}
	for name, code := range codesOf(t, db, ent) {
		if code != "" {
			t.Errorf("%s получил код %q без --write", name, code)
		}
	}
}

// С --write заполняются только пустые: команда дозаполняет, а не
// перенумеровывает — иначе она переписала бы коды, на которые уже ссылаются
// документы и внешние системы.
func TestRenumber_FillsOnlyEmpty(t *testing.T) {
	ent := renumberCatalog()
	db := renumberDB(t, ent, []map[string]any{
		{"Наименование": "Альфа", metadata.StandardCodeField: "СТАРЫЙ-1"},
		{"Наименование": "Бета"},
		{"Наименование": "Гамма"},
	})

	rep, err := renumberEntity(context.Background(), db, ent, true)
	if err != nil {
		t.Fatalf("renumberEntity: %v", err)
	}
	if rep.Filled != 2 {
		t.Errorf("заполнено %d, ожидалось 2", rep.Filled)
	}
	codes := codesOf(t, db, ent)
	if codes["Альфа"] != "СТАРЫЙ-1" {
		t.Errorf("существующий код переписан: %q", codes["Альфа"])
	}
	for _, name := range []string{"Бета", "Гамма"} {
		if !strings.HasPrefix(codes[name], "К-") {
			t.Errorf("%s не получил код: %q", name, codes[name])
		}
	}
	if codes["Бета"] == codes["Гамма"] {
		t.Errorf("выдан одинаковый код обеим записям: %q", codes["Бета"])
	}
}

// Повторный запуск ничего не делает: заполнять больше нечего.
func TestRenumber_Idempotent(t *testing.T) {
	ent := renumberCatalog()
	db := renumberDB(t, ent, []map[string]any{{"Наименование": "Альфа"}})

	if _, err := renumberEntity(context.Background(), db, ent, true); err != nil {
		t.Fatalf("первый прогон: %v", err)
	}
	before := codesOf(t, db, ent)["Альфа"]

	rep, err := renumberEntity(context.Background(), db, ent, true)
	if err != nil {
		t.Fatalf("второй прогон: %v", err)
	}
	if rep.Filled != 0 || rep.Empty != 0 {
		t.Errorf("повторный прогон что-то нашёл: %+v", rep)
	}
	if after := codesOf(t, db, ent)["Альфа"]; after != before {
		t.Errorf("код изменился при повторном прогоне: %q → %q", before, after)
	}
}

// Объект без numerator: в цели не попадает — раздача кодов всем справочникам
// молча изменила бы данные существующих конфигураций.
// База больше одной страницы: раньше renumberEntity делал единственный List с
// Limit=MaxListPageSize и молча оставлял хвост пустым, рапортуя успех (#867).
// Справочник иерархический — заодно ловим второй капкан: List без Sort сортирует
// иерархические по is_folder и первому строковому полю, то есть по заполняемому
// «Коду», и страницы Offset-пейджинга плыли бы прямо под записью.
func TestRenumber_FillsBeyondFirstPage(t *testing.T) {
	ent := renumberCatalog()
	ent.Hierarchical = true
	total := storage.MaxListPageSize + 100
	seed := make([]map[string]any, 0, total)
	for i := 0; i < total; i++ {
		seed = append(seed, map[string]any{"Наименование": fmt.Sprintf("Элемент %04d", i)})
	}
	db := renumberDB(t, ent, seed)

	rep, err := renumberEntity(context.Background(), db, ent, true)
	if err != nil {
		t.Fatalf("renumberEntity: %v", err)
	}
	if rep.Empty != total || rep.Filled != total {
		t.Errorf("отчёт = %+v, ожидалось empty=filled=%d", rep, total)
	}

	rows, err := db.List(context.Background(), ent.Name, ent, storage.ListParams{Sort: "id", Limit: 0})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != total {
		t.Fatalf("прочитано %d строк, ожидалось %d", len(rows), total)
	}
	uniq := map[string]bool{}
	blank := 0
	for _, r := range rows {
		code := strings.TrimSpace(asAnyString(rowFieldValue(r, metadata.StandardCodeField)))
		if code == "" {
			blank++
			continue
		}
		uniq[code] = true
	}
	if blank != 0 {
		t.Errorf("остались записи без кода: %d", blank)
	}
	if len(uniq) != total {
		t.Errorf("кодов %d уникальных, ожидалось %d — есть дубли", len(uniq), total)
	}
}

func TestRenumberTargets_SkipsWithoutNumerator(t *testing.T) {
	withNum := renumberCatalog()
	plain := &metadata.Entity{
		Name: "Склады", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	proj := &project.Project{Entities: []*metadata.Entity{withNum, plain}}

	targets, err := renumberTargets(proj, "")
	if err != nil {
		t.Fatalf("renumberTargets: %v", err)
	}
	if len(targets) != 1 || targets[0].Name != "Контрагенты" {
		t.Fatalf("цели = %v, ожидались только Контрагенты", names(targets))
	}

	if _, err := renumberTargets(proj, "Склады"); err == nil {
		t.Error("объект без numerator: принят как цель")
	}
}

func names(ents []*metadata.Entity) []string {
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name)
	}
	return out
}

// Полный путь команды: проект на диске + база; без --write ничего не меняется.
func TestRunRenumber_EndToEndDryRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "name: Контрагенты\nnumerator: {prefix: \"К-\", length: 6}\nfields:\n  - {name: Наименование, type: string}\n"
	if err := os.WriteFile(filepath.Join(dir, "catalogs", "контрагенты.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	ent := renumberCatalog()
	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": "Альфа"}, ent); err != nil {
		t.Fatal(err)
	}
	db.Close()

	cmd := renumberCmd
	if err := cmd.Flags().Set("project", dir); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("sqlite", dbPath); err != nil {
		t.Fatal(err)
	}
	if err := runRenumber(cmd, nil); err != nil {
		t.Fatalf("runRenumber: %v", err)
	}

	db2, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db2.Close)
	if code := codesOf(t, db2, ent)["Альфа"]; code != "" {
		t.Errorf("без --write код проставлен: %q", code)
	}
}
