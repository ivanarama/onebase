package rowsource

import (
	"context"
	"errors"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/shopspring/decimal"
)

func TestRowSourceCapturedInputsUsesCanonicalTypedSelection(t *testing.T) {
	ctx, db, reg, _, _, id := fixture(t)
	rep := reg.GetReport("RowSource")
	rep.RowSource = registeredDefinition()
	rep.Params = []report.Param{{Name: "Limit", Type: "number"}}
	rep.Query = initialQuery + " ГДЕ Р.Amount=&Limit"
	accepted := InputSelection{Parameters: map[string]AcceptedParameter{"Limit": {Text: "125.50", Parsed: decimal.RequireFromString("125.50")}}}
	calls := 0
	frame, err := CaptureInputFrame(ctx, db, reg, "rowsource", accepted, InputSelection{}, func(_ context.Context, name string) (string, error) {
		if name != "RowSource" {
			t.Fatal("uncanonical module key")
		}
		calls++
		return enrichCode, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted.Parameters["Limit"] = AcceptedParameter{Text: "0", Parsed: decimal.Zero}
	result, err := RunCapturedInputs(ctx, db, reg, frame)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(result.Rows) != 1 || result.Rows[0]["request"] != id.String() || result.Rows[0]["Works"] != "Работа А; \nРабота А; \nЗаметка для клиента" {
		t.Fatal("canonical query/typed snapshot/enrichment changed")
	}
	if result.queryEvidence == nil || len(result.queryEvidence.reads) != 1 {
		t.Fatal("actual additional Query evidence missing")
	}
}
func TestRowSourceCapturedInputsRejectsStaleBeforeExecution(t *testing.T) {
	for _, mode := range []string{"module", "query", "metadata", "source", "actor", "cancel", "zero", "foreign db"} {
		t.Run(mode, func(t *testing.T) {
			ctx, db, reg, ent, _, _ := fixture(t)
			rep := reg.GetReport("RowSource")
			rep.RowSource = registeredDefinition()
			code := enrichCode
			frame, err := CaptureInputFrame(ctx, db, reg, "RowSource", InputSelection{}, InputSelection{}, func(context.Context, string) (string, error) { return code, nil })
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "module":
				code += " // changed"
			case "query":
				rep.Query += " ГДЕ Р.Count=1"
			case "metadata":
				ent.Fields[0].PII = true
			case "source":
				rep.RowSource.Columns[0].Type = "number"
			case "actor":
				ctx = auth.ContextWithUser(ctx, &auth.User{Login: "other", IsAdmin: true})
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "zero":
				frame = &InputFrame{}
			case "foreign db":
				_, otherDB, _, _, _, _ := fixture(t)
				db = otherDB
			}
			result, err := RunCapturedInputs(ctx, db, reg, frame)
			if err == nil || result != nil {
				t.Fatal("stale or forged input selection executed")
			}
		})
	}
}
func TestRowSourceCapturedInputsRejectsPostExecutionChangesAtomically(t *testing.T) {
	for _, mode := range []string{"loader failure", "module", "query", "roles", "metadata", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			base, db, reg, ent, _, _ := fixture(t)
			ctx, cancel := context.WithCancel(base)
			defer cancel()
			user := &auth.User{Login: "reader", IsAdmin: true}
			ctx = auth.ContextWithUser(ctx, user)
			rep := reg.GetReport("RowSource")
			rep.RowSource = registeredDefinition()
			calls := 0
			loader := func(context.Context, string) (string, error) {
				calls++
				if calls == 3 {
					switch mode {
					case "loader failure":
						return "", errors.New("synthetic unavailable module")
					case "module":
						return enrichCode + " // changed", nil
					case "query":
						rep.Query += " ГДЕ Р.Count=1"
					case "roles":
						user.IsAdmin = false
					case "metadata":
						ent.Fields[1].PII = true
					case "cancel":
						cancel()
					}
				}
				return enrichCode, nil
			}
			frame, err := CaptureInputFrame(ctx, db, reg, "RowSource", InputSelection{}, InputSelection{}, loader)
			if err != nil {
				t.Fatal(err)
			}
			result, err := RunCapturedInputs(ctx, db, reg, frame)
			if calls != 3 || err == nil || result != nil {
				t.Fatal("post-execution failure leaked partial successful result")
			}
		})
	}
}
