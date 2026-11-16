package resultfilter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/resultfilter"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

func TestReportResultFilterBeforeLimitMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{Name: "FilterProbe", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Title", Type: "string"}, {Name: "Amount", Type: "number"}, {Name: "Active", Type: "bool"}, {Name: "Moment", Type: "date"}, {Name: "Secret", Type: "string", PII: true}}}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
		for i, n := range []string{"1", "2", "9007199254740993.01", "9007199254740993.02"} {
			if err := db.Upsert(ctx, entity.Name, ids[i], map[string]any{"Title": string(rune('a' + i)), "Amount": decimal.RequireFromString(n), "Active": true, "Moment": time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC), "Secret": "private"}, entity); err != nil {
				t.Fatal(err)
			}
		}
		res, err := query.Compile("ВЫБРАТЬ П.Title КАК label, П.Amount КАК amount, П.Active КАК active, П.Moment КАК moment, П.Secret КАК innocuous, П.Ссылка КАК rowid ИЗ Справочник.FilterProbe КАК П УПОРЯДОЧИТЬ ПО П.Title", query.CompileOpts{Entities: []*metadata.Entity{entity}, Dialect: db.Dialect()})
		if err != nil {
			t.Fatal(err)
		}
		lookup := func(kind, name string) *metadata.Entity {
			if kind == "catalog" && name == entity.Name {
				return entity
			}
			return nil
		}
		allow := func(_ query.SourceRef, m *metadata.Entity, name string, _ bool) bool {
			for _, f := range m.Fields {
				if f.Name == name {
					return !f.PII
				}
			}
			return name == "Ссылка"
		}
		fields := resultfilter.Schema(res, lookup, allow)
		if len(fields) != 5 {
			t.Fatalf("proven fields: %+v; occurrences=%+v", fields, res.SourceOccurrences)
		}
		if _, err := resultfilter.New(fields, []reportpkg.Filter{{Field: "innocuous", Op: "eq", Value: "private"}}, nil); err == nil {
			t.Fatal("PII alias allowed")
		}
		filters := []reportpkg.Filter{{Field: "amount", Op: "gt", Value: "9007199254740993.00"}, {Field: "active", Op: "eq", Value: "true"}, {Field: "moment", Op: "ge", Value: "2026-11-16T12:00:00Z"}}
		plan, err := resultfilter.New(fields, filters, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := resultfilter.Run(ctx, db, &res, plan, resultfilter.Options{MaxRows: 1, CountAll: true})
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != 2 || len(got.Rows) != 1 || got.Rows[0]["label"] != "c" || !got.Truncated {
			t.Fatalf("before cap: %+v", got)
		}
		plan, err = resultfilter.New(fields, []reportpkg.Filter{{Field: "amount", Op: "eq", Value: "9007199254740993.02"}, {Field: "rowid", Op: "eq", Value: ids[3].String()}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err = resultfilter.Run(ctx, db, &res, plan, resultfilter.Options{MaxRows: 1})
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != 1 || len(got.Rows) != 1 || got.Rows[0]["label"] != "d" || got.Truncated {
			t.Fatalf("precision/UUID: %+v", got)
		}
		got, err = resultfilter.Run(ctx, db, &res, plan, resultfilter.Options{MaxRows: 1, MaxScan: 2})
		if !errors.Is(err, resultfilter.ErrScanLimit) || len(got.Rows) != 0 {
			t.Fatalf("incomplete totals escaped: %+v, %v", got, err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err = resultfilter.Run(canceled, db, &res, plan, resultfilter.Options{}); err == nil {
			t.Fatal("cancellation ignored")
		}
	})
}

func TestReportResultFilterPublicSchemaAndValidation(t *testing.T) {
	entity := &metadata.Entity{Name: "FilterProbe", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Title", Type: "string"}, {Name: "Amount", Type: "number"}}}
	lookup := func(_, name string) *metadata.Entity {
		if name == entity.Name {
			return entity
		}
		return nil
	}
	allowed := func(query.SourceRef, *metadata.Entity, string, bool) bool { return true }
	for _, src := range []string{
		"ВЫБРАТЬ ПЕРВЫЕ 1 П.Title КАК alias ИЗ Справочник.FilterProbe КАК П",
		"ВЫБРАТЬ П.Title КАК alias, П.Amount КАК alias ИЗ Справочник.FilterProbe КАК П",
		"ВЫБРАТЬ П.Title КАК alias, П.Amount+1 КАК alias ИЗ Справочник.FilterProbe КАК П",
		"ВЫБРАТЬ П.* ИЗ Справочник.FilterProbe КАК П",
		"ВЫБРАТЬ П.Title + 'x' КАК alias ИЗ Справочник.FilterProbe КАК П",
	} {
		res, err := query.Compile(src, query.CompileOpts{Entities: []*metadata.Entity{entity}})
		if err != nil {
			t.Fatal(err)
		}
		if fields := resultfilter.Schema(res, lookup, allowed); len(fields) != 0 {
			t.Fatalf("unproven projection: %s %+v", src, fields)
		}
	}
	fields := []resultfilter.Field{{Name: "n", Type: "number", Operators: []string{"eq", "gt"}}, {Name: "b", Type: "bool", Operators: []string{"eq"}}, {Name: "s", Type: "string", Operators: []string{"eq", "contains", "ne", "is_null"}}}
	for _, f := range []reportpkg.Filter{{Field: "missing", Op: "eq", Value: "1"}, {Field: "b", Op: "gt", Value: "true"}, {Field: "b", Op: "eq", Value: "anything"}, {Field: "n", Op: "eq", Value: "1e999999999"}, {Field: "n", Op: "eq", Value: "NaN"}, {Field: "s", Op: "SQL", Value: "x"}, {Field: "s", Op: "is_null", Value: "x"}} {
		if _, err := resultfilter.New(fields, []reportpkg.Filter{f}, nil); err == nil {
			t.Fatalf("invalid condition accepted: %+v", f)
		}
	}
	for _, tc := range []struct {
		filter reportpkg.Filter
		row    map[string]any
		want   bool
	}{
		{reportpkg.Filter{Field: "s", Op: "ne", Value: ""}, map[string]any{"s": nil}, false},
		{reportpkg.Filter{Field: "s", Op: "is_null"}, map[string]any{"s": nil}, true},
		{reportpkg.Filter{Field: "s", Op: "eq", Value: ""}, map[string]any{"s": ""}, true},
		{reportpkg.Filter{Field: "s", Op: "contains", Value: "%_'"}, map[string]any{"s": "a%_'b"}, true},
		{reportpkg.Filter{Field: "s", Op: "contains", Value: "ПрИвЕт"}, map[string]any{"s": "пример: ПРИВЕТ"}, true},
	} {
		p, err := resultfilter.New(fields, []reportpkg.Filter{tc.filter}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Match(tc.row)
		if err != nil || got != tc.want {
			t.Fatalf("match=%v err=%v", got, err)
		}
	}
}
