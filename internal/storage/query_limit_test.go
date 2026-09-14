package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunQueryLimit_Truncates(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, `CREATE TABLE items(id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, err := db.Exec(ctx, `INSERT INTO items(name) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
	}

	rows, cols, truncated, err := db.RunQueryLimit(ctx, `SELECT name FROM items ORDER BY id`, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(rows) != 2 {
		t.Fatalf("rows len = %d, want 2", len(rows))
	}
	if len(cols) != 1 || cols[0] != "name" {
		t.Fatalf("cols = %+v, want [name]", cols)
	}
}

func TestRunQueryLimit_Disabled(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, `CREATE TABLE items(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO items(id) VALUES (1), (2)`); err != nil {
		t.Fatal(err)
	}

	rows, _, truncated, err := db.RunQueryLimit(ctx, `SELECT id FROM items ORDER BY id`, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("truncated = true, want false")
	}
	if len(rows) != 2 {
		t.Fatalf("rows len = %d, want 2", len(rows))
	}
}

func TestRunQueryPage_WrapsExplicitLimitAndTrailingSemicolon(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, `CREATE TABLE items(id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c", "d"} {
		if _, err := db.Exec(ctx, `INSERT INTO items(name) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
	}

	query := `SELECT name FROM items WHERE name >= ? ORDER BY id LIMIT 3;`
	total, err := db.CountQuery(ctx, query, []any{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	rows, cols, err := db.RunQueryPage(ctx, query, []any{"a"}, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["name"] != "c" {
		t.Fatalf("rows = %#v, want c", rows)
	}
	if len(cols) != 1 || cols[0] != "name" {
		t.Fatalf("cols = %#v, want [name]", cols)
	}
}

func TestLimitedQuerySQL_AddsLimitWhenMissing(t *testing.T) {
	got := limitedQuerySQL(`SELECT id FROM items ORDER BY id;`, 11)
	want := `SELECT id FROM items ORDER BY id LIMIT 11`
	if got != want {
		t.Fatalf("limitedQuerySQL = %q, want %q", got, want)
	}
}

func TestLimitedQuerySQL_KeepsExplicitTopLevelLimit(t *testing.T) {
	query := `SELECT id FROM items ORDER BY id LIMIT 5`
	if got := limitedQuerySQL(query, 11); got != query {
		t.Fatalf("limitedQuerySQL changed explicit limit: %q", got)
	}
}

func TestLimitedQuerySQL_IgnoresNestedLimit(t *testing.T) {
	got := limitedQuerySQL(`SELECT * FROM (SELECT id FROM items LIMIT 5) x`, 11)
	if strings.Count(strings.ToLower(got), " limit ") != 2 {
		t.Fatalf("expected outer limit to be added, got %q", got)
	}
}

func TestLimitedQuerySQL_IgnoresLimitInStringAndComment(t *testing.T) {
	got := limitedQuerySQL("SELECT 'limit' AS word -- limit in comment\nFROM items", 11)
	if !strings.HasSuffix(got, " LIMIT 11") {
		t.Fatalf("expected outer limit suffix, got %q", got)
	}
}
