package rowsource

// Native boundary conformance cases grant no admission for native A004/A005.
// Execution evidence is recorded separately for an exact source commit.
import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/shopspring/decimal"
)

// A same-named real Count field was used by the early positive fixture. It does
// not prove a native SQL literal1 or a DateDiff expression, even outside DSL.
func TestRowSourceNativeExpressionIsNotProvenByOutputAlias(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"literal replaces Count", strings.Replace(initialQuery, "Р.Count КАК Count", "1 КАК Count", 1)},
		{"DateDiff replaces Moment", strings.Replace(initialQuery, "Р.Moment КАК Moment", "РАЗНОСТЬДАТ(Р.Moment,&End,ДЕНЬ) КАК Moment", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db, reg, _, _, _ := fixture(t)
			rep := reg.GetReport("RowSource")
			rep.Query = tc.query
			rep.RowSource = registeredDefinition()
			rep.RowSource.Inputs = []string{"Request", "Note"}
			rep.Params = []report.Param{{Name: "End", Type: "datetime"}}
			end := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
			params := map[string]any{"End": end}
			// The ordinary query compiler must support the query. An unrelated parser
			// failure would not demonstrate the specific provenance boundary.
			if _, err := query.Compile(tc.query, query.CompileOpts{Entities: reg.Entities(), Dialect: db.Dialect(), Params: params}); err != nil {
				t.Fatal("fixture is not a supported ordinary query", err)
			}
			loader := func(context.Context, string) (string, error) { return enrichCode, nil }
			result, err := RunRegistered(ctx, db, reg, "RowSource", Query{Text: tc.query, Params: params}, loader)
			if err == nil || result != nil || !strings.Contains(err.Error(), "тип или происхождение") {
				t.Fatal("alias/exclusion substituted for expression proof", err)
			}
			frame, err := CaptureInputFrame(ctx, db, reg, "RowSource", InputSelection{Parameters: map[string]AcceptedParameter{"End": {Text: "2026-11-30T00:00:00Z", Parsed: end}}}, InputSelection{}, loader)
			if err == nil || frame != nil {
				t.Fatal("unproven expression entered captured frame")
			}
		})
	}
}

func TestRowSourceNativeReferenceLabelAndRootPIICannotBecomeUUIDProof(t *testing.T) {
	for _, mode := range []string{"bare protected label", "proven UUID", "PII root with UUID"} {
		t.Run(mode, func(t *testing.T) {
			ctx, db, reg, ent, _, id := fixture(t)
			target := &metadata.Entity{Name: "NativeTarget", Kind: metadata.KindCatalog, Presentation: []string{"Label"}, Fields: []metadata.Field{{Name: "Label", Type: "string"}, {Name: "Phone", Type: "string", PII: true}}}
			field := metadata.Field{Name: "Target", Type: "reference:NativeTarget", RefEntity: target.Name, PII: mode == "PII root with UUID"}
			ent.Fields = append(ent.Fields, field)
			if err := db.Migrate(ctx, []*metadata.Entity{target, ent}); err != nil {
				t.Fatal(err)
			}
			targetID := uuid.New()
			if err := db.Upsert(ctx, target.Name, targetID, map[string]any{"Label": "SYNTHETIC_PROTECTED_LABEL", "Phone": "SYNTHETIC_PRIVATE"}, target); err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(ctx, ent.Name, id, map[string]any{"Label": "safe", "Note": "safe-note", "Amount": decimal.RequireFromString("125.50"), "Count": decimal.NewFromInt(1), "Moment": time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC), "Owner": "reader", "Target": targetID.String()}, ent); err != nil {
				t.Fatal(err)
			}
			rep := reg.GetReport("RowSource")
			rep.RowSource = registeredDefinition()
			rep.RowSource.Inputs = []string{"Request", "Note"}
			path := "Р.Target.Ссылка"
			if mode == "bare protected label" {
				path = "Р.Target"
			}
			rep.Query = strings.Replace(initialQuery, "Р.Note КАК Note", "Р.Note КАК Note,"+path+" КАК TargetResult", 1)
			reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent, target}, Reports: []*report.Report{rep}})
			// Admin is deliberately not an exemption from the report output policy.
			ctx = auth.ContextWithUser(ctx, &auth.User{Login: "synthetic-admin", IsAdmin: true})
			result, err := RunRegistered(ctx, db, reg, "RowSource", Query{Text: rep.Query}, func(context.Context, string) (string, error) { return enrichCode, nil })
			if mode != "proven UUID" {
				if err == nil || result != nil {
					t.Fatal("presentation or protected root field bypassed output policy")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Rows) != 1 || result.Rows[0]["targetresult"] != targetID.String() {
					t.Fatal("proven UUID changed")
				}
				for _, row := range result.Rows {
					for _, value := range row {
						if text, ok := value.(string); ok && strings.Contains(text, "SYNTHETIC_PROTECTED") {
							t.Fatal("label entered the safe original channel")
						}
					}
				}
			}
		})
	}
}
