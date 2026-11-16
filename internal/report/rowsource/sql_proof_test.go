package rowsource

// Captured SQL-proof conformance cases. Execution evidence is recorded by exact
// source commit and requires the assigned Go lane plus the fresh disk barrier.
import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report/resultfilter"
	"github.com/shopspring/decimal"
)

func TestRowSourceCapturedSQLProofRecordsEveryRead(t *testing.T) {
	ctx, db, reg, _, _, _ := fixture(t)
	rep := reg.GetReport("RowSource")
	rep.RowSource = registeredDefinition()
	rep.RowSource.Inputs = []string{"Request", "Note"}
	frame, err := CaptureInputFrame(ctx, db, reg, "RowSource", InputSelection{}, InputSelection{}, func(context.Context, string) (string, error) { return enrichCode, nil })
	if err != nil {
		t.Fatal(err)
	}
	if frame.state.identity.SQLProof == [32]byte{} {
		t.Fatal("SQL proof omitted from input frame")
	}
	result, err := RunCapturedInputs(ctx, db, reg, frame)
	if err != nil {
		t.Fatal(err)
	}
	if result.queryEvidence == nil || result.queryEvidence.frame == nil || *result.queryEvidence.frame != frame.state.identity || len(result.queryEvidence.sqlReads) != 2 {
		t.Fatal("initial/additional read receipts missing")
	}
	for _, read := range result.queryEvidence.sqlReads {
		if read.receipt == nil || read.inspection == [32]byte{} {
			t.Fatal("missing immutable SQL receipt")
		}
	}
}
func TestRowSourceCapturedSQLProofRequiresUniqueParameterBinding(t *testing.T) {
	for _, mode := range []string{"initial missing", "additional case collision"} {
		t.Run(mode, func(t *testing.T) {
			ctx, db, reg, _, _, _ := fixture(t)
			rep := reg.GetReport("RowSource")
			rep.RowSource = registeredDefinition()
			code := enrichCode
			if mode == "initial missing" {
				rep.Query += " ГДЕ Р.Amount=&Missing"
			} else {
				code = strings.Replace(code, `З.УстановитьПараметр("ID",Р.Request);`, `З.УстановитьПараметр("ID",Р.Request);З.УстановитьПараметр("id",Р.Request);`, 1)
			}
			frame, err := CaptureInputFrame(ctx, db, reg, "RowSource", InputSelection{}, InputSelection{}, func(context.Context, string) (string, error) { return code, nil })
			if mode == "initial missing" {
				if err == nil || frame != nil {
					t.Fatal("missing host parameter binding accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := RunCapturedInputs(ctx, db, reg, frame)
			if err == nil || result != nil {
				t.Fatal("ambiguous additional parameter binding accepted")
			}
		})
	}
}
func TestRowSourceSQLProofReadRevalidatesIntegrityAndAuthorization(t *testing.T) {
	for _, mode := range []string{"typed args", "metadata", "actor", "no receipt", "timezone"} {
		t.Run(mode, func(t *testing.T) {
			ctx, db, reg, ent, _, _ := fixture(t)
			reg.GetReport("RowSource").RowSource = registeredDefinition()
			user := &auth.User{Login: "reader", IsAdmin: true}
			ctx = auth.ContextWithUser(ctx, user)
			e := &execution{db: db, reg: reg, lookup: resultfilter.RegistryLookup(reg), fields: map[string][]resultfilter.Field{}, originKeys: map[string]bool{}, requireSQLProof: true, report: "RowSource"}
			res, err := e.compile(ctx, initialQuery+" ГДЕ Р.Amount=&Amount", map[string]any{"Amount": decimal.RequireFromString("125.50")})
			if err != nil {
				t.Fatal(err)
			}
			args := append([]any(nil), res.Args...)
			switch mode {
			case "typed args":
				if len(args) != 1 {
					t.Fatal("bad argument fixture")
				}
				if _, ok := args[0].(string); ok {
					args[0] = decimal.RequireFromString("125.50")
				} else {
					args[0] = "125.50"
				}
			case "metadata":
				ent.Fields[1].PII = true
			case "actor":
				user.IsAdmin = false
			case "no receipt":
				e.sqlProofs = nil
			case "timezone":
				before := time.Local
				time.Local = time.FixedZone("changed-proof-test", 3600)
				t.Cleanup(func() { time.Local = before })
			}
			rows, err := e.QueryAll(ctx, res.SQL, args...)
			if err == nil || rows != nil || len(e.sqlReads) != 0 {
				t.Fatal("stale/unauthorized SQL proof reached read")
			}
		})
	}
}
func TestRowSourceSQLProofRejectsForgedReceipt(t *testing.T) {
	ctx, db, reg, _, _, _ := fixture(t)
	e := &execution{db: db, reg: reg, lookup: resultfilter.RegistryLookup(reg)}
	opts, err := e.compileOptions(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := query.Compile(initialQuery, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []*query.ReportLineage{nil, {}} {
		if proof, err := e.guardSQLProof(ctx, res, opts, receipt); err == nil || proof != nil {
			t.Fatal("public/zero descriptor minted private SQL proof")
		}
	}
}
func TestRowSourceCapturedSQLProofChecksUnprojectedControls(t *testing.T) {
	ctx, db, reg, _, _, _ := fixture(t)
	rep := reg.GetReport("RowSource")
	rep.RowSource = registeredDefinition()
	rep.Query += " ГДЕ Р.Owner=&Owner"
	// Reuse the strict compiler directly to inspect every membership field in a
	// successful private receipt, rather than claiming projection origins suffice.
	e := &execution{db: db, reg: reg, lookup: resultfilter.RegistryLookup(reg), fields: map[string][]resultfilter.Field{}, originKeys: map[string]bool{}, requireSQLProof: true, report: "RowSource"}
	_, err := e.compile(ctx, rep.Query, map[string]any{"Owner": "reader"})
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	var visit func(query.ReportExpression)
	visit = func(x query.ReportExpression) {
		for _, origin := range x.Origins {
			seen = seen || origin.Field == "Owner"
		}
		for _, arg := range x.Args {
			visit(arg)
		}
	}
	for _, use := range e.lastSQLProof.receipt.Uses() {
		visit(use.Expression)
	}
	if !seen {
		t.Fatal("unprojected membership dependency lost")
	}
}
