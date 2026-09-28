package storage_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

type choiceFilterFixture struct {
	direction *metadata.Entity
	target    *metadata.Entity
	root      uuid.UUID
	child     uuid.UUID
	grand     uuid.UUID
	sibling   uuid.UUID
	rootRow   uuid.UUID
	childRow  uuid.UUID
	grandRow  uuid.UUID
}

func choiceTestUUID(suffix string) uuid.UUID {
	return uuid.MustParse("00000000-0000-0000-0000-" + suffix)
}

func seedChoiceFilterFixture(t *testing.T, db *storage.DB) choiceFilterFixture {
	t.Helper()
	ctx := context.Background()
	direction := &metadata.Entity{
		Name: "ChoiceDirection", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	target := &metadata.Entity{
		Name: "ChoiceFault", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Направление", Type: metadata.FieldType("reference:" + direction.Name), RefEntity: direction.Name},
			{Name: "Owner", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{direction, target}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	fixture := choiceFilterFixture{
		direction: direction,
		target:    target,
		root:      choiceTestUUID("000000000001"),
		child:     choiceTestUUID("000000000002"),
		grand:     choiceTestUUID("000000000003"),
		sibling:   choiceTestUUID("000000000004"),
		rootRow:   choiceTestUUID("000000000101"),
		childRow:  choiceTestUUID("000000000102"),
		grandRow:  choiceTestUUID("000000000103"),
	}
	for _, row := range []struct {
		id     uuid.UUID
		name   string
		parent *uuid.UUID
	}{
		{fixture.root, "root", nil},
		{fixture.child, "child", &fixture.root},
		{fixture.grand, "grand", &fixture.child},
		{fixture.sibling, "sibling", nil},
	} {
		fields := map[string]any{"Наименование": row.name, "ЭтоГруппа": true}
		if row.parent != nil {
			fields["Родитель"] = row.parent.String()
		}
		if err := db.Upsert(ctx, direction.Name, row.id, fields, direction); err != nil {
			t.Fatalf("seed direction %s: %v", row.name, err)
		}
	}

	for _, row := range []struct {
		id        uuid.UUID
		name      string
		direction uuid.UUID
		owner     string
		folder    bool
	}{
		{fixture.rootRow, "root alpha", fixture.root, "alice", false},
		{fixture.childRow, "child alpha", fixture.child, "alice", false},
		{fixture.grandRow, "grand alpha", fixture.grand, "alice", false},
		{choiceTestUUID("000000000104"), "sibling alpha", fixture.sibling, "alice", false},
		{choiceTestUUID("000000000105"), "child blocked", fixture.child, "bob", false},
		{choiceTestUUID("000000000106"), "folder alpha", fixture.child, "alice", true},
	} {
		if err := db.Upsert(ctx, target.Name, row.id, map[string]any{
			"Наименование": row.name,
			"Направление":  row.direction.String(),
			"Owner":        row.owner,
			"ЭтоГруппа":    row.folder,
		}, target); err != nil {
			t.Fatalf("seed target %s: %v", row.name, err)
		}
	}
	return fixture
}

func choiceRowNames(rows []map[string]any) []string {
	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = fmt.Sprint(row["Наименование"])
	}
	return names
}

func choiceRowIDs(t *testing.T, rows []map[string]any) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		id, err := uuid.Parse(fmt.Sprint(row["id"]))
		if err != nil {
			t.Fatalf("row %d id %v: %v", i, row["id"], err)
		}
		ids[i] = id
	}
	return ids
}

func assertChoiceListAndCount(t *testing.T, db *storage.DB, fixture choiceFilterFixture, params storage.ListParams, wantCount int) []map[string]any {
	t.Helper()
	rows, err := db.List(context.Background(), fixture.target.Name, fixture.target, params)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	total, err := db.CountList(context.Background(), fixture.target.Name, fixture.target, params)
	if err != nil {
		t.Fatalf("CountList: %v", err)
	}
	if total != wantCount {
		t.Fatalf("CountList = %d, want %d; rows=%v", total, wantCount, choiceRowNames(rows))
	}
	return rows
}

// Choice predicates change SQL semantics, so the same public DB.List and
// DB.CountList calls run on SQLite and PostgreSQL. Besides the plan's tree and
// folder cases, the combined case protects argument numbering across legacy
// Filters/Search, choice predicates, RLS and keyset bounds.
func TestChoiceFilterPredicatesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		fixture := seedChoiceFilterFixture(t, db)
		base := []storage.ChoicePredicate{
			{Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, Value: fixture.root},
			{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: false},
		}

		t.Run("root descendants sibling and pagination total", func(t *testing.T) {
			rows := assertChoiceListAndCount(t, db, fixture, storage.ListParams{
				ChoicePredicates: base,
				Limit:            2,
			}, 4)
			if len(rows) != 2 {
				t.Fatalf("page rows = %d, want 2: %v", len(rows), choiceRowNames(rows))
			}
			for _, name := range choiceRowNames(rows) {
				if strings.Contains(name, "sibling") || strings.Contains(name, "folder") {
					t.Fatalf("foreign branch/folder leaked into page: %v", choiceRowNames(rows))
				}
			}

			all := assertChoiceListAndCount(t, db, fixture, storage.ListParams{ChoicePredicates: base}, 4)
			got := strings.Join(choiceRowNames(all), ",")
			for _, want := range []string{"root alpha", "child alpha", "grand alpha", "child blocked"} {
				if !strings.Contains(got, want) {
					t.Errorf("root/subtree result %q misses %q", got, want)
				}
			}
			if strings.Contains(got, "sibling") || strings.Contains(got, "folder") {
				t.Fatalf("root/subtree result contains sibling/folder: %q", got)
			}
		})

		t.Run("eq is parameterized", func(t *testing.T) {
			rows := assertChoiceListAndCount(t, db, fixture, storage.ListParams{ChoicePredicates: []storage.ChoicePredicate{
				{Field: "Направление", Op: metadata.FormChoiceOpEqual, Value: fixture.child.String()},
				{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: false},
			}}, 2)
			if got := strings.Join(choiceRowNames(rows), ","); !strings.Contains(got, "child alpha") || !strings.Contains(got, "child blocked") {
				t.Fatalf("eq rows = %q", got)
			}
		})

		t.Run("explicit folder scope replaces implicit picker scope", func(t *testing.T) {
			folders := assertChoiceListAndCount(t, db, fixture, storage.ListParams{
				ExcludeFolders: true,
				ChoicePredicates: []storage.ChoicePredicate{{
					Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: true,
				}},
			}, 1)
			if len(folders) != 1 || fmt.Sprint(folders[0]["Наименование"]) != "folder alpha" {
				t.Fatalf("is_folder:true rows = %v", choiceRowNames(folders))
			}

			items := assertChoiceListAndCount(t, db, fixture, storage.ListParams{
				OnlyFolders: true,
				ChoicePredicates: []storage.ChoicePredicate{{
					Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: false,
				}},
			}, 5)
			if len(items) != 5 {
				t.Fatalf("is_folder:false rows = %d, want 5", len(items))
			}
		})

		t.Run("missing root is empty", func(t *testing.T) {
			rows := assertChoiceListAndCount(t, db, fixture, storage.ListParams{ChoicePredicates: []storage.ChoicePredicate{
				{Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, Value: uuid.New()},
			}}, 0)
			if len(rows) != 0 {
				t.Fatalf("missing root returned %v", choiceRowNames(rows))
			}
		})

		t.Run("filters search RLS and keyset stay ANDed", func(t *testing.T) {
			rows := assertChoiceListAndCount(t, db, fixture, storage.ListParams{
				Filters:          map[string]storage.FilterValue{"Наименование": {Value: "alpha"}},
				Search:           "alpha",
				ChoicePredicates: base,
				RowFilter:        &storage.Predicate{Field: "Owner", Op: "eq", Value: "alice"},
				AfterID:          &fixture.rootRow,
				ThroughID:        &fixture.grandRow,
				Limit:            10,
			}, 3) // CountList intentionally reports the total before keyset pagination.
			if got, want := choiceRowIDs(t, rows), []uuid.UUID{fixture.childRow, fixture.grandRow}; !reflect.DeepEqual(got, want) {
				t.Fatalf("keyset rows = %v, want %v", got, want)
			}
		})

		t.Run("invalid UUID fails closed", func(t *testing.T) {
			for name, value := range map[string]any{
				"malformed": "not-a-uuid' OR 1=1 --",
				"zero":      uuid.Nil,
				"nil":       (*uuid.UUID)(nil),
			} {
				t.Run(name, func(t *testing.T) {
					params := storage.ListParams{ChoicePredicates: []storage.ChoicePredicate{{
						Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, Value: value,
					}}}
					if rows, err := db.List(context.Background(), fixture.target.Name, fixture.target, params); err == nil || len(rows) != 0 || !strings.Contains(err.Error(), "UUID") {
						t.Fatalf("List invalid UUID: rows=%v err=%v", rows, err)
					}
					if total, err := db.CountList(context.Background(), fixture.target.Name, fixture.target, params); err == nil || total != 0 || !strings.Contains(err.Error(), "UUID") {
						t.Fatalf("CountList invalid UUID: total=%d err=%v", total, err)
					}
				})
			}
		})

		t.Run("exact membership reuses choice and row predicates", func(t *testing.T) {
			params := storage.ListParams{
				ChoicePredicates: base,
				RowFilter:        &storage.Predicate{Field: "Owner", Op: "eq", Value: "alice"},
			}
			allowed, err := db.ListContainsID(context.Background(), fixture.target.Name, fixture.target, fixture.grandRow, params)
			if err != nil || !allowed {
				t.Fatalf("allowed subtree row: allowed=%v err=%v", allowed, err)
			}
			blocked, err := db.ListContainsID(context.Background(), fixture.target.Name, fixture.target, choiceTestUUID("000000000105"), params)
			if err != nil || blocked {
				t.Fatalf("RLS-hidden row: allowed=%v err=%v", blocked, err)
			}
			foreign, err := db.ListContainsID(context.Background(), fixture.target.Name, fixture.target, choiceTestUUID("000000000104"), params)
			if err != nil || foreign {
				t.Fatalf("foreign hierarchy row: allowed=%v err=%v", foreign, err)
			}
		})

		t.Run("cycle terminates without duplicates", func(t *testing.T) {
			dialect := db.Dialect()
			arg := func(id uuid.UUID) any {
				if dialect.Name() == "sqlite" {
					return id.String()
				}
				return id
			}
			query := fmt.Sprintf("UPDATE %s SET parent_id = %s WHERE id = %s",
				metadata.TableName(fixture.direction.Name), dialect.Placeholder(1), dialect.Placeholder(2))
			if _, err := db.Exec(context.Background(), query, arg(fixture.grand), arg(fixture.root)); err != nil {
				t.Fatalf("corrupt hierarchy into cycle: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			params := storage.ListParams{ChoicePredicates: base}
			rows, err := db.List(ctx, fixture.target.Name, fixture.target, params)
			if err != nil {
				t.Fatalf("List on cyclic hierarchy: %v", err)
			}
			if len(rows) != 4 {
				t.Fatalf("cycle rows = %d, want 4 without duplicates: %v", len(rows), choiceRowNames(rows))
			}
			total, err := db.CountList(ctx, fixture.target.Name, fixture.target, params)
			if err != nil || total != 4 {
				t.Fatalf("CountList on cycle = %d, %v; want 4", total, err)
			}
		})
	})
}
