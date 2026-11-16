package query_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func reportLineageEntities() []*metadata.Entity {
	return []*metadata.Entity{
		{Name: "LineageRequests", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Number", Type: "string"}, {Name: "Phone", Type: "string", PII: true}, {Name: "Owner", Type: "string"},
			{Name: "Branch", Type: "reference:LineageBranches", RefEntity: "LineageBranches"},
		}, TableParts: []metadata.TablePart{{Name: "Rows", Fields: []metadata.Field{{Name: "Amount", Type: "number"}}}}},
		{Name: "LineageBranches", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Title", Type: "string"}}},
	}
}

const reportLineageMatchQuery = `ВЫБРАТЬ
 ВЫБОР КОГДА МИНИМУМ(B.Number)<A.Number ТОГДА МИНИМУМ(B.Number) ИНАЧЕ A.Number КОНЕЦ КАК GroupID,
 A.Ссылка КАК Request, КОЛИЧЕСТВО(*) КАК Matches
 ИЗ Документ.LineageRequests КАК A
 ВНУТРЕННЕЕ СОЕДИНЕНИЕ Документ.LineageRequests КАК B ПО A.Ссылка<>B.Ссылка И A.Phone=B.Phone И A.Phone<>""
 ГДЕ A.Owner=&Owner
 СГРУППИРОВАТЬ ПО A.Number,A.Ссылка
 УПОРЯДОЧИТЬ ПО GroupID`

func walkReportExpression(e query.ReportExpression, visit func(query.ReportExpression)) {
	visit(e)
	for _, arg := range e.Args {
		walkReportExpression(arg, visit)
	}
}

func TestReportLineageMatchingMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entities := reportLineageEntities()
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
		for i, id := range ids {
			phone := "synthetic-key-A"
			owner := "reader"
			if i == 2 {
				owner = "other"
			}
			if err := db.Upsert(ctx, entities[0].Name, id, map[string]any{"Number": []string{"R1", "R2", "R3"}[i], "Phone": phone, "Owner": owner}, entities[0]); err != nil {
				t.Fatal(err)
			}
		}
		opts := query.CompileOpts{Entities: entities, Dialect: db.Dialect(), Params: map[string]any{"Owner": "reader"}, RowFilters: map[query.SourceRef]*storage.Predicate{{Kind: "document", Name: "LineageRequests"}: {Field: "Owner", Op: "eq", Value: "reader"}}}
		res, proof, err := query.CompileReportLineage(reportLineageMatchQuery, opts)
		if err != nil {
			t.Fatal(err)
		}
		if !proof.Matches(res, opts) {
			t.Fatal("fresh receipt does not match")
		}
		// Both JOIN sides must be RLS-filtered before matching. The excluded third
		// request must neither appear nor increase the business count.
		rows, _, err := query.Run(ctx, db, &res)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 {
			t.Fatalf("matching grain changed: %d", len(rows))
		}
		for _, row := range rows {
			if row["groupid"] != "R1" || strings.Contains(strings.ToLower(strings.TrimSpace(row["request"].(string))), ids[2].String()) {
				t.Fatal("RLS or grouping escaped")
			}
			if row["matches"] != int64(1) {
				t.Fatalf("excluded JOIN side influenced count: %v", row["matches"])
			}
		}
		seen := map[int]bool{}
		parameter := false
		for _, use := range proof.Uses() {
			walkReportExpression(use.Expression, func(e query.ReportExpression) {
				if e.Parameter == "Owner" {
					parameter = true
				}
				for _, o := range e.Origins {
					if o.Field == "Phone" {
						seen[o.Occurrence] = true
						if o.Source.Kind != "document" || o.Type != "string" {
							t.Fatal("source/type lost")
						}
					}
				}
			})
		}
		if !seen[0] || !seen[1] || !parameter {
			t.Fatal("self JOIN occurrence or parameter provenance lost")
		}
		for _, column := range proof.Outputs() {
			walkReportExpression(column.Expression, func(e query.ReportExpression) {
				for _, o := range e.Origins {
					if o.Field == "Phone" {
						t.Fatal("internal phone became output lineage")
					}
				}
			})
		}
		// A receipt grants no rights. Ordinary role masking still rejects this
		// protected comparison; mandatory reporting policy belongs to the host.
		plan := access.QueryMaskPlanFor(&auth.User{}, res, func(kind, name string) *metadata.Entity {
			for _, ent := range entities {
				if string(ent.Kind) == kind && ent.Name == name {
					return ent
				}
			}
			return nil
		})
		if plan.Denied == "" {
			t.Fatal("lineage silently authorized protected equality")
		}
	})
}

func TestReportLineageRejectsUnknownAndAmbiguous(t *testing.T) {
	opts := query.CompileOpts{Entities: reportLineageEntities()}
	for name, text := range map[string]string{
		"derived":               "ВЫБРАТЬ Q.Number КАК N ИЗ (ВЫБРАТЬ A.Number КАК Number ИЗ Документ.LineageRequests КАК A) КАК Q",
		"union":                 "ВЫБРАТЬ A.Number КАК N ИЗ Документ.LineageRequests КАК A ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ A.Number ИЗ Документ.LineageRequests КАК A",
		"wildcard":              "ВЫБРАТЬ A.* ИЗ Документ.LineageRequests КАК A",
		"unknown_function":      "ВЫБРАТЬ Magical(A.Phone) КАК N ИЗ Документ.LineageRequests КАК A",
		"unknown_field":         "ВЫБРАТЬ A.Absent КАК N ИЗ Документ.LineageRequests КАК A",
		"unknown_qualifier":     "ВЫБРАТЬ B.Number КАК N ИЗ Документ.LineageRequests КАК A",
		"bare_ref_presentation": "ВЫБРАТЬ A.Branch КАК N ИЗ Документ.LineageRequests КАК A",
		"duplicate_output":      "ВЫБРАТЬ A.Number КАК N,A.Phone КАК N ИЗ Документ.LineageRequests КАК A",
		"ambiguous_bare":        "ВЫБРАТЬ Number КАК N ИЗ Документ.LineageRequests КАК A ВНУТРЕННЕЕ СОЕДИНЕНИЕ Документ.LineageRequests КАК B ПО A.Phone=B.Phone",
		"duplicate_qualifier":   "ВЫБРАТЬ A.Number КАК N ИЗ Документ.LineageRequests КАК A ВНУТРЕННЕЕ СОЕДИНЕНИЕ Документ.LineageRequests КАК A ПО A.Phone=A.Phone",
		"unary_ordinal":         "ВЫБРАТЬ A.Phone КАК HiddenPhone,A.Number КАК N ИЗ Документ.LineageRequests КАК A УПОРЯДОЧИТЬ ПО +1",
		"unary_group_ordinal":   "ВЫБРАТЬ A.Phone КАК HiddenPhone ИЗ Документ.LineageRequests КАК A СГРУППИРОВАТЬ ПО +1",
		"ordinal":               "ВЫБРАТЬ A.Number КАК N ИЗ Документ.LineageRequests КАК A УПОРЯДОЧИТЬ ПО 1",
		"trailing":              "ВЫБРАТЬ A.Number КАК N ИЗ Документ.LineageRequests КАК A INVALID",
	} {
		t.Run(name, func(t *testing.T) {
			res, p, err := query.CompileReportLineage(text, opts)
			if err == nil || p != nil || res.SQL != "" {
				t.Fatal("unknown syntax returned partial receipt")
			}
		})
	}
	duplicate := reportLineageEntities()
	duplicate = append(duplicate, duplicate[0])
	opts.Entities = duplicate
	if _, p, err := query.CompileReportLineage("ВЫБРАТЬ A.Number КАК N ИЗ Документ.LineageRequests КАК A", opts); err == nil || p != nil {
		t.Fatal("duplicate metadata accepted")
	}
}

func TestReportLineageNamespaceAndStaleRLS(t *testing.T) {
	entities := reportLineageEntities()
	entities = append(entities, &metadata.Entity{Name: entities[0].Name, Kind: metadata.KindCatalog, Fields: entities[0].Fields})
	if _, p, err := query.CompileReportLineage("ВЫБРАТЬ A.Number КАК N ИЗ Документ.LineageRequests КАК A", query.CompileOpts{Entities: entities}); err == nil || p != nil {
		t.Fatal("catalog/document sameName supplied a different compiler metadata type")
	}
	opts := query.CompileOpts{Entities: reportLineageEntities(), Params: map[string]any{"Owner": "reader"}}
	res, p, err := query.CompileReportLineage("ВЫБРАТЬ A.Number КАК N ИЗ Документ.LineageRequests КАК A ГДЕ A.Owner=&Owner", opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.RowFilters = map[query.SourceRef]*storage.Predicate{{Kind: "document", Name: "LineageRequests"}: {Field: "Owner", Op: "eq", Value: "other"}}
	if p.Matches(res, opts) {
		t.Fatal("stale RLS receipt accepted")
	}
	opts.RowFilters = nil
	opts.Params["Owner"] = "other"
	if p.Matches(res, opts) {
		t.Fatal("changed input binding accepted")
	}
}

func TestReportLineageReceiptAndReferenceRoot(t *testing.T) {
	opts := query.CompileOpts{Entities: reportLineageEntities(), Params: map[string]any{"Owner": "reader"}}
	res, p, err := query.CompileReportLineage("ВЫБРАТЬ A.Branch.Ссылка КАК BranchID,A.Branch.Title КАК BranchTitle ИЗ Документ.LineageRequests КАК A ГДЕ A.Owner=&Owner", opts)
	if err != nil {
		t.Fatal(err)
	}
	columns := p.Outputs()
	if len(columns) != 2 || len(columns[0].Expression.Origins) != 2 {
		t.Fatal("navigation root not retained")
	}
	origins := columns[0].Expression.Origins
	if origins[0].Field != "Branch" || origins[0].Source.Name != "LineageRequests" || origins[1].Field != "id" || origins[1].Source.Name != "LineageBranches" {
		t.Fatalf("wrong root: %+v", origins)
	}
	columns[0].Expression.Origins[0].Field = "forged"
	if p.Outputs()[0].Expression.Origins[0].Field != "Branch" || !p.Matches(res, opts) {
		t.Fatal("inspection view mutated receipt")
	}
	changed := res
	changed.SQL += " LIMIT 1"
	if p.Matches(changed, opts) {
		t.Fatal("stale SQL proof accepted")
	}
	changed = res
	changed.Args = append([]any(nil), res.Args...)
	changed.Args[0] = "other"
	if p.Matches(changed, opts) {
		t.Fatal("stale parameters accepted")
	}
	opts.Entities[0].Fields[0].PII = true
	if p.Matches(res, opts) {
		t.Fatal("stale metadata policy accepted")
	}
}

func TestReportLineageClassifierAndTablepart(t *testing.T) {
	opts := query.CompileOpts{Entities: reportLineageEntities()}
	for _, text := range []string{
		"ВЫБРАТЬ A.Number КАК N ИЗ Документ.LineageRequests КАК A ГДЕ SUBSTR(ЕСТЬNULL(A.Phone,\"\"),5,1)<>\"\"",
		"ВЫБРАТЬ T.Ссылка КАК Request,T.Amount КАК Amount ИЗ Документ.LineageRequests.Rows КАК T",
	} {
		_, p, err := query.CompileReportLineage(text, opts)
		if err != nil {
			t.Fatal(err)
		}
		if p == nil {
			t.Fatal("missing receipt")
		}
	}
}
