package query_test

import (
	"fmt"
	"testing"

	"github.com/ivantit66/onebase/internal/query"
)

func TestReportLineageReservedOutputAliases(t *testing.T) {
	opts := query.CompileOpts{Entities: reportLineageEntities()}
	for _, alias := range []string{"Ссылка", "Ref", "Reference", "id"} {
		for _, clause := range []string{"group", "order"} {
			t.Run(alias+"_"+clause, func(t *testing.T) {
				text := fmt.Sprintf(`SELECT A.Phone AS %s,COUNT(*) AS N FROM Document.LineageRequests AS A GROUP BY %s`, alias, alias)
				if clause == "order" {
					text = fmt.Sprintf(`SELECT A.Number AS N,A.Phone AS %s FROM Document.LineageRequests AS A ORDER BY %s`, alias, alias)
				}
				res, p, err := query.CompileReportLineage(text, opts)
				if err == nil || p != nil || res.SQL != "" {
					t.Fatal("reserved reference output alias returned a receipt instead of fail-closed STOP")
				}
			})
		}
	}
}
func TestReportLineageOrdinaryReferenceProjectionControl(t *testing.T) {
	opts := query.CompileOpts{Entities: reportLineageEntities()}
	res, p, err := query.CompileReportLineage(`SELECT A.Ссылка AS Request,COUNT(*) AS N FROM Document.LineageRequests AS A GROUP BY A.Ссылка ORDER BY Request`, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Matches(res, opts) {
		t.Fatal("fresh ordinary reference receipt rejected")
	}
	out := p.Outputs()
	if len(out) != 2 || out[0].Name != "request" || len(out[0].Expression.Origins) != 1 || out[0].Expression.Origins[0].Field != "id" || out[0].Expression.Type != "reference:LineageRequests" {
		t.Fatal("ordinary UUID/reference provenance changed")
	}
}
