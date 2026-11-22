package interpreter

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/sheet"
	"github.com/shopspring/decimal"
)

func TestTabularPayloadSnapshot(t *testing.T) {
	d := NewSpreadsheetDocument()
	w := d.getCellObj(1, 1).(*SpreadsheetDocumentCellWrapper)
	amount := decimal.RequireFromString("123456789012345.12345")
	date := time.Date(2026, 11, 15, 12, 13, 14, 123, time.FixedZone("own", 3*3600))
	row := NewStructFromMap(map[string]any{"amount": amount, "date": date, "count": int64(math.MaxInt64), "flag": false, "empty": nil, "text": "server_only_payload", "master": &Ref{UUID: "00000000-0000-0000-0000-000000000123", Type: "Own", Kind: metadata.KindCatalog}})
	a := &Array{items: []any{row}}
	w.Set("расшифровка", a)
	a.items = append(a.items, &Array{items: []any{"added_before_publish"}})
	frozen := d.Doc.Snapshot()
	n, err := FreezeTabularDrilldowns(frozen)
	if err != nil || n == 0 {
		t.Fatalf("freeze: %v", err)
	}
	row.Set("amount", 0)
	a.items = nil
	got, err := RestoreTabularDrilldown(frozen.GetCell(0, 0).DrilldownPayload, func(r *sheet.Reference, kind string) (any, error) {
		return &Ref{UUID: r.ID, Type: r.Entity, Kind: metadata.Kind(kind)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	items := got.(*Array)
	if len(items.items) != 2 {
		t.Fatal("pre-publication append lost")
	}
	record := items.items[0].(*Struct)
	if !record.Get("amount").(decimal.Decimal).Equal(amount) || !record.Get("date").(time.Time).Equal(date) || record.Get("count") != int64(math.MaxInt64) || record.Get("flag") != false || record.Get("empty") != nil {
		t.Fatal("scalar type/value changed")
	}
	if frozen.GetCell(0, 0).DrilldownSource != nil {
		t.Fatal("builder/context retained")
	}
	wire, err := json.Marshal(frozen.GetCell(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "server_only_payload") {
		t.Fatal("payload serialized into cell JSON")
	}
	record.Set("amount", 0)
	again, err := RestoreTabularDrilldown(frozen.GetCell(0, 0).DrilldownPayload, func(r *sheet.Reference, kind string) (any, error) { return &Ref{UUID: r.ID, Type: r.Entity}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if !again.(*Array).items[0].(*Struct).Get("amount").(decimal.Decimal).Equal(amount) {
		t.Fatal("callback mutated snapshot")
	}
}

func TestTabularPayloadRefusesUnsafe(t *testing.T) {
	cycle := &Array{}
	cycle.items = []any{cycle}
	for _, tt := range []struct {
		name  string
		value any
	}{{"cycle", cycle}, {"function", func() {}}, {"capability", &MapThis{M: map[string]any{}}}, {"nan", math.NaN()}, {"infinity", math.Inf(1)}, {"large", strings.Repeat("x", TabularPayloadMaxCellBytes+1)}} {
		t.Run(tt.name, func(t *testing.T) {
			d := NewSpreadsheetDocument()
			d.getCellObj(1, 1).(*SpreadsheetDocumentCellWrapper).Set("расшифровка", tt.value)
			if _, err := FreezeTabularDrilldowns(d.Doc.Snapshot()); err == nil {
				t.Fatal("unsafe payload accepted")
			}
			if d.Doc.GetCell(0, 0).DrilldownSource == nil {
				t.Fatal("source changed on failed publication")
			}
		})
	}
}
