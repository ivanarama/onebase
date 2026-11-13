package ui

import (
	"bytes"
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/xuri/excelize/v2"
)

func TestListCellExactFilterHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		target := &metadata.Entity{Name: "Targets", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		ent := &metadata.Entity{Name: "Items", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Amount", Type: metadata.FieldTypeNumber},
			{Name: "Flag", Type: metadata.FieldTypeBool}, {Name: "Day", Type: metadata.FieldTypeDate},
			{Name: "Target", Type: "reference:Targets", RefEntity: "Targets"},
		}}
		s := newSubmitTestServerOn(t, db, []*metadata.Entity{target, ent})
		ctx := context.Background()
		id1, id2 := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{id1, id2} {
			if err := db.Upsert(ctx, target.Name, id, map[string]any{"Наименование": "Same label"}, target); err != nil {
				t.Fatal(err)
			}
		}
		for _, row := range []map[string]any{
			{"Наименование": "Exact%_", "Amount": 5, "Flag": false, "Day": time.Date(2026, 11, 12, 12, 0, 0, 0, time.Local), "Target": id1.String()},
			{"Наименование": "prefixExact%_suffix", "Amount": 50, "Flag": true, "Day": time.Date(2026, 11, 13, 12, 0, 0, 0, time.Local), "Target": id2.String()},
			{"Наименование": "Other", "Amount": 5, "Flag": false, "Day": time.Date(2026, 11, 12, 21, 0, 0, 0, time.Local), "Target": id2.String()},
		} {
			if err := db.Upsert(ctx, ent.Name, uuid.New(), row, ent); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct {
			q    url.Values
			want int
		}{
			{url.Values{"f.Наименование.eq": {"Exact%_"}}, 1},
			{url.Values{"f.Amount.eq": {"5"}}, 2},
			{url.Values{"f.Amount.eq": {"05.00"}}, 2},
			{url.Values{"f.Amount.eq": {"not-number"}}, 0},
			{url.Values{"f.Flag.eq": {"false"}}, 2},
			{url.Values{"f.Target.eq": {id1.String()}}, 1},
			{url.Values{"f.Day.from": {"2026-11-12"}, "f.Day.to": {"2026-11-12"}}, 2},
		} {
			r := reqWithChi("GET", "/ui/catalog/Items?"+tc.q.Encode(), nil, map[string]string{"entity": "Items"})
			rec := httptest.NewRecorder()
			s.list(rec, r)
			if rec.Code != 200 {
				t.Fatalf("%v: %d %s", tc.q, rec.Code, rec.Body.String())
			}
			if got := strings.Count(rec.Body.String(), "data-ob-entity-id="); got != tc.want {
				t.Fatalf("%v: %d rows, want %d", tc.q, got, tc.want)
			}
			if tc.want > 0 && !strings.Contains(rec.Body.String(), "data-ob-cell=") {
				t.Fatal("no filterable cells")
			}
			export := httptest.NewRecorder()
			s.listExcel(export, reqWithChi("GET", "/ui/catalog/Items/excel?"+tc.q.Encode(), nil, map[string]string{"entity": "Items"}))
			if export.Code != 200 {
				t.Fatalf("excel: %d %s", export.Code, export.Body.String())
			}
			book, err := excelize.OpenReader(bytes.NewReader(export.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			rows, err := book.GetRows(book.GetSheetName(0))
			book.Close()
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != tc.want+1 {
				t.Fatalf("%v: excel rows=%v", tc.q, rows)
			}
		}
	})
}
