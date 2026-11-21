package rowsource

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/shopspring/decimal"
)

func frameFixture(t *testing.T) (context.Context, func(InputSelection, InputSelection) (*InputFrame, error), *runtime.Registry) {
	t.Helper()
	ctx, db, reg, _, _, _ := fixture(t)
	rep := reg.GetReport("RowSource")
	rep.RowSource = registeredDefinition()
	rep.Params = []report.Param{{Name: "Amount", Type: "number"}, {Name: "Flag", Type: "bool"}, {Name: "Note", Type: "string"}}
	rep.Variants = []report.ReportVariant{{Name: "Grouped", Composition: &report.Composition{Groupings: []string{"Amount"}}}}
	user := &auth.User{ID: "synthetic-reader", Login: "reader", Roles: []*auth.Role{{Name: "R", Permissions: auth.Permission{Reports: map[string][]string{"RowSource": {"run"}}, Documents: map[string][]string{"RowRequest": {"read"}}}}}}
	ctx = auth.ContextWithUser(ctx, user)
	capture := func(a, b InputSelection) (*InputFrame, error) {
		return CaptureInputFrame(ctx, db, reg, "rowsource", a, b, func(context.Context, string) (string, error) { return enrichCode, nil })
	}
	return ctx, capture, reg
}
func TestRowSourceInputFrameKindPresenceAndMetadataBinding(t *testing.T) {
	ctx, capture, reg := frameFixture(t)
	empty := ""
	request := InputSelection{Parameters: map[string]AcceptedParameter{
		"amount": {Text: "0", Parsed: decimal.Zero}, "Flag": {Text: "false", Parsed: false}, "Note": {Text: "", Parsed: nil},
	}, Variant: &empty}
	f, err := capture(request, InputSelection{})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := f.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]InputBindingSummary{}
	for _, b := range summary.Bindings {
		seen[b.ID] = b
	}
	for _, id := range []string{"0:Amount", "1:Flag"} {
		if seen[id].Kind != "user-input" || seen[id].Presence != "value" {
			t.Fatal("zero/false treated as missing")
		}
	}
	if seen["2:Note"].Presence != "explicit-empty" || seen["variant"].Presence != "explicit-empty" {
		t.Fatal("explicit empty lost")
	}
	if err := f.Revalidate(ctx, f.state.db, reg); err != nil {
		t.Fatal(err)
	}
	request.Parameters["amount"] = AcceptedParameter{Text: "999", Parsed: decimal.NewFromInt(999)}
	for i := 0; i < 5; i++ {
		if err := f.Revalidate(ctx, f.state.db, reg); err != nil {
			t.Fatal("external input mutation/map order affected frame", err)
		}
	}
	summary.Bindings[0].Kind = "fixed-literal"
	fresh, _ := f.Inspect()
	if fresh.Bindings[0].Kind == "fixed-literal" {
		t.Fatal("inspection mutated private frame")
	}
}
func TestRowSourceInputFrameEqualBytesRetainKindsAndChannel(t *testing.T) {
	_, capture, _ := frameFixture(t)
	accepted := InputSelection{Parameters: map[string]AcceptedParameter{"Flag": {Text: "false", Parsed: false}}}
	user, err := capture(accepted, InputSelection{})
	if err != nil {
		t.Fatal(err)
	}
	preset, err := capture(InputSelection{}, accepted)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := capture(InputSelection{}, InputSelection{})
	if err != nil {
		t.Fatal(err)
	}
	find := func(f *InputFrame) inputBinding {
		for _, b := range f.state.inputs {
			if b.ID == "1:Flag" && (b.Presence != "missing" || b.Kind != "user-input") {
				return b
			}
		}
		t.Fatal("missing binding")
		return inputBinding{}
	}
	a, b, c := find(user), find(preset), find(defaults)
	if a.Value != b.Value || a.Value != c.Value || a.Kind == c.Kind || a.Channel == b.Channel || c.Presence != "missing" || !c.UserControlled {
		t.Fatal("equal value cleansed origin/presence")
	}
	if user.state.identity.Inputs == defaults.state.identity.Inputs || user.state.identity.Inputs == preset.state.identity.Inputs {
		t.Fatal("binding omitted from frame")
	}
}
func TestRowSourceInputFrameMarkersNeverProveDefault(t *testing.T) {
	_, capture, _ := frameFixture(t)
	f, err := capture(InputSelection{Parameters: map[string]AcceptedParameter{"__has.Note": {Text: "default"}}}, InputSelection{Parameters: map[string]AcceptedParameter{"Note": {Text: "old", Parsed: "old"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range f.state.inputs {
		if b.ID == "2:Note" && (b.Kind != "user-input" || b.Channel != "request" || b.Presence != "explicit-empty") {
			t.Fatal("client marker became default")
		}
	}
}
func TestRowSourceInputFrameRejectsDescriptorsDefaultsAndUnknowns(t *testing.T) {
	for _, mode := range []string{"unknown", "duplicate", "wrong_type", "descriptor", "context_default"} {
		t.Run(mode, func(t *testing.T) {
			_, capture, reg := frameFixture(t)
			in := InputSelection{Parameters: map[string]AcceptedParameter{}}
			switch mode {
			case "unknown":
				in.Parameters["origin"] = AcceptedParameter{Text: "default", Parsed: "default"}
			case "duplicate":
				in.Parameters["Flag"] = AcceptedParameter{Parsed: false}
				in.Parameters["flag"] = AcceptedParameter{Parsed: false}
			case "wrong_type":
				in.Parameters["Amount"] = AcceptedParameter{Text: "1", Parsed: "1"}
			case "descriptor":
				in.Parameters["Note"] = AcceptedParameter{Parsed: map[string]any{"Type": "string", "Origin": "fixed-literal", "Value": "x"}}
			case "context_default":
				reg.GetReport("RowSource").Params[2].Default = "{{now}}"
			}
			f, err := capture(in, InputSelection{})
			if err == nil || f != nil {
				t.Fatal("unbound/forged input accepted")
			}
		})
	}
}
func TestRowSourceInputFrameStaleActorMetadataModuleAndRLS(t *testing.T) {
	for _, mode := range []string{"actor", "role", "metadata", "module", "query", "generation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, capture, reg := frameFixture(t)
			f, err := capture(InputSelection{}, InputSelection{})
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "actor":
				ctx = auth.ContextWithUser(ctx, &auth.User{Login: "other", IsAdmin: true})
			case "role":
				delete(auth.UserFromContext(ctx).Roles[0].Permissions.Reports, "RowSource")
			case "metadata":
				reg.GetEntity("RowRequest").Fields[1].PII = true
			case "module":
				f.state.code = func(context.Context, string) (string, error) { return enrichCode + "\n// new module", nil }
			case "query":
				reg.GetReport("RowSource").Query = initialQuery + " ГДЕ Р.Count>0"
			case "generation":
				reg.Load(runtime.LoadOptions{})
			}
			if err := f.Revalidate(ctx, f.state.db, reg); err == nil {
				t.Fatal("stale frame accepted")
			}
		})
	}
}
func TestRowSourceInputFrameNotTransportOrAuthority(t *testing.T) {
	_, capture, _ := frameFixture(t)
	secret := "synthetic_private_input"
	f, err := capture(InputSelection{Parameters: map[string]AcceptedParameter{"Note": {Text: secret, Parsed: secret}}}, InputSelection{})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{f, *f} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if got := fmt.Sprintf(format, v); got != "ReportInputFrame" || strings.Contains(got, secret) {
				t.Fatal("host state formatting")
			}
		}
		if _, err := json.Marshal(v); err == nil {
			t.Fatal("serialized frame accepted")
		}
	}
	var forged InputFrame
	if err := json.Unmarshal([]byte(`{"state":{"Kind":"fixed-literal"}}`), &forged); err == nil {
		t.Fatal("JSON minted frame")
	}
	if _, err := forged.Inspect(); err == nil {
		t.Fatal("zero frame acquired evidence")
	}
}

func TestRowSourceInputFrameRejectsMixedCapture(t *testing.T) {
	for _, change := range []string{"metadata", "actor"} {
		t.Run(change, func(t *testing.T) {
			ctx, _, reg := frameFixture(t)
			_, db, _, _, _, _ := fixture(t)
			f, err := CaptureInputFrame(ctx, db, reg, "RowSource", InputSelection{}, InputSelection{}, func(context.Context, string) (string, error) {
				if change == "metadata" {
					reg.GetReport("RowSource").Params[0].Name = "Renamed"
				} else {
					auth.UserFromContext(ctx).Lang = "changed"
				}
				return enrichCode, nil
			})
			if err == nil || f != nil {
				t.Fatal("mixed metadata/actor epoch captured")
			}
		})
	}
}
func TestRowSourceInputFrameBindsRLSAndCodec(t *testing.T) {
	ctx, capture, reg := frameFixture(t)
	u := auth.UserFromContext(ctx)
	u.Roles[0].Permissions.RowAccess = auth.RowAccess{Documents: map[string]auth.RowPolicies{"RowRequest": {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}}}}
	f, err := capture(InputSelection{}, InputSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if f.state.identity.CodecFormat != 2 {
		t.Fatal("codec format omitted")
	}
	if err := f.Revalidate(ctx, f.state.db, reg); err != nil {
		t.Fatal(err)
	}
	u.Roles[0].Permissions.RowAccess.Documents["RowRequest"] = auth.RowPolicies{"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "id"}}}
	if err := f.Revalidate(ctx, f.state.db, reg); err == nil {
		t.Fatal("changed RLS binding accepted")
	}
}
func TestRowSourceInputFrameMissingDefaultHasControlBindings(t *testing.T) {
	_, capture, _ := frameFixture(t)
	f, err := capture(InputSelection{}, InputSelection{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range f.state.inputs {
		if b.Kind == "server-default" && b.Slot == "report-param" {
			if b.DefaultExpression == [32]byte{} || len(b.Controls) != 2 || !b.UserControlled {
				t.Fatal("default cleaned missing user controls")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing defaults not represented")
	}
}
func TestRowSourceInputFrameVariantAndRefRoot(t *testing.T) {
	_, capture, reg := frameFixture(t)
	variant := "gRoUpEd"
	f, err := capture(InputSelection{Variant: &variant}, InputSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if f.state.identity.Variant != "Grouped" {
		t.Fatal("variant not canonical")
	}
	unknown := "other"
	if f, err := capture(InputSelection{Variant: &unknown}, InputSelection{}); err == nil || f != nil {
		t.Fatal("unknown variant accepted")
	}
	reg.GetReport("RowSource").Params = append(reg.GetReport("RowSource").Params, report.Param{Name: "Request", Type: "reference:RowRequest"})
	f, err = capture(InputSelection{Parameters: map[string]AcceptedParameter{"Request": {Text: "00000000-0000-0000-0000-000000000000", Parsed: "00000000-0000-0000-0000-000000000000"}}}, InputSelection{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range f.state.inputs {
		if b.ID == "3:Request" && b.Kind == "user-input" && b.RefRoot == "RowRequest" {
			found = true
		}
	}
	if !found {
		t.Fatal("reference root/binding lost")
	}
	reg.GetReport("RowSource").Params[3].Type = "reference:Unknown"
	if f, err := capture(InputSelection{}, InputSelection{}); err == nil || f != nil {
		t.Fatal("unknown reference root accepted")
	}
}
