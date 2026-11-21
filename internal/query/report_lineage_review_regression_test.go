package query_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
	"testing"
)

func TestPlatform2ReportLineageGroupAliasCollision(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		entities := reportLineageEntities()
		ctx := context.Background()
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		for _, phone := range []string{"synthetic-A", "synthetic-B"} {
			if err := db.Upsert(ctx, entities[0].Name, uuid.New(), map[string]any{"Number": "PUBLIC", "Phone": phone}, entities[0]); err != nil {
				t.Fatal(err)
			}
		}
		res, p, err := query.CompileReportLineage(`SELECT "PUBLIC" AS Phone, COUNT(*) AS N FROM Document.LineageRequests AS A GROUP BY Phone`, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
		if err != nil {
			t.Logf("collision stopped: %v", err)
			return
		}
		rows, _, err := query.Run(ctx, db, &res)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("SQL=%s rows=%v Uses=%+v", res.SQL, rows, p.Uses())
		seen := false
		for _, u := range p.Uses() {
			walkReportExpression(u.Expression, func(e query.ReportExpression) {
				for _, o := range e.Origins {
					if o.Field == "Phone" {
						seen = true
					}
				}
			})
		}
		if len(rows) == 2 && !seen {
			t.Fatal("protected physical GROUP BY dependency disappeared behind public output alias")
		}
	})
}
func TestPlatform2ReportLineageArgumentTypeReceipt(t *testing.T) {
	opts := query.CompileOpts{Entities: reportLineageEntities(), Params: map[string]any{"Owner": decimal.RequireFromString("1")}}
	res, p, err := query.CompileReportLineage(`SELECT A.Number AS N FROM Document.LineageRequests AS A WHERE A.Owner=&Owner`, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("args before=%#v", res.Args)
	changed := res
	changed.Args = append([]any(nil), res.Args...)
	for i := range changed.Args {
		changed.Args[i] = "1"
	}
	if p.Matches(changed, opts) {
		t.Fatal("changed SQL argument decimal->string accepted as identical receipt")
	}
}

func TestPlatform2ReportLineagePositiveReceipt(t *testing.T) {
	opts := query.CompileOpts{Entities: reportLineageEntities(), Params: map[string]any{"Owner": "reader"}}
	res, p, err := query.CompileReportLineage(`SELECT A.Number AS N FROM Document.LineageRequests AS A WHERE A.Owner=&Owner`, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Matches(res, opts) {
		t.Fatal("fresh receipt rejected")
	}
	changed := res
	changed.Args = append([]any(nil), res.Args...)
	changed.Args[0] = "other"
	if p.Matches(changed, opts) {
		t.Fatal("changed argument value accepted")
	}
	outputs := p.Outputs()
	outputs[0].Expression.Origins[0].Field = "forged"
	if !p.Matches(res, opts) || p.Outputs()[0].Expression.Origins[0].Field != "Number" {
		t.Fatal("detached view changed private receipt")
	}
}

// Independent platform counterexamples use MIN output and a simultaneous
// args/environment base64/bytes collision, rather than the literal/decimal case.
func TestReportLineagePlatformCounterexamples(t *testing.T) {
	opts := query.CompileOpts{Entities: reportLineageEntities(), Params: map[string]any{"Owner": "eA=="}}
	_, p, err := query.CompileReportLineage(`SELECT MIN(A.Number) AS Phone,COUNT(*) AS N FROM Document.LineageRequests AS A GROUP BY Phone`, opts)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, u := range p.Uses() {
		walkReportExpression(u.Expression, func(e query.ReportExpression) {
			for _, o := range e.Origins {
				if o.Field == "Phone" {
					seen = true
				}
			}
		})
	}
	if !seen {
		t.Fatal("input Phone replaced by MIN(Number) output alias")
	}
	res, p, err := query.CompileReportLineage(`SELECT A.Number AS N FROM Document.LineageRequests AS A WHERE A.Owner=&Owner`, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Matches(res, opts) {
		t.Fatal("fresh receipt rejected")
	}
	changed := res
	changed.Args = append([]any(nil), res.Args...)
	changed.Args[0] = []byte("x")
	opts.Params["Owner"] = []byte("x")
	if p.Matches(changed, opts) {
		t.Fatal("simultaneous args/environment string->bytes JSON collision accepted")
	}
}
