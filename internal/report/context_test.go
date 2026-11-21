package report_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

type contextReader struct {
	value storage.UserCardValue
	calls int
	actor string
	err   error
}

func (r *contextReader) ReadUserCardSource(_ context.Context, _ storage.UserCardDescriptor, actor string) (storage.UserCardValue, error) {
	r.calls++
	r.actor = actor
	return r.value, r.err
}

func TestReportTrustedContext(t *testing.T) {
	id := uuid.NewString()
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{{Name: "Staff", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Account", Type: "reference:_users", RefEntity: "_users"}, {Name: "Department", Type: metadata.FieldTypeString},
	}}}})
	rep := &report.Report{Params: []report.Param{
		{Name: "Actor", Type: "string", Context: &report.ParamContext{User: "login"}},
		{Name: "ActorID", Type: "reference:_users", Context: &report.ParamContext{User: "id"}},
		{Name: "Unit", Type: "string", Context: &report.ParamContext{UserCard: &auth.UserCardSource{Catalog: "Staff", AccountField: "Account", Field: "Department"}}},
		{Name: "Input", Type: "string"},
	}}
	ctx := auth.ContextWithUser(context.Background(), &auth.User{ID: id, Login: "alice"})
	reader := &contextReader{value: storage.UserCardValue{Available: true, Value: "OWN"}}
	input := map[string]any{"Actor": "bob", "actor": "evil", "ACTORID": uuid.NewString(), "Unit": "OTHER", "Input": "keep"}
	got, err := report.ResolveContextParams(ctx, rep, input, reg, reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got["Actor"] != "alice" || got["ActorID"] != id || got["Unit"] != "OWN" || got["Input"] != "keep" || reader.actor != id || reader.calls != 1 {
		t.Fatalf("incorrect trusted bindings: %#v", got)
	}
	if input["Actor"] != "bob" || input["actor"] != "evil" {
		t.Fatal("caller map mutated")
	}
	if len(rep.InputParams()) != 1 || rep.InputParams()[0].Name != "Input" {
		t.Fatal("context is editable")
	}
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		reader *contextReader
	}{
		{"missing-actor", context.Background(), reader},
		{"missing-card", ctx, &contextReader{}},
		{"reader-error", ctx, &contextReader{err: errors.New("private SQL failure")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := report.ResolveContextParams(tc.ctx, rep, input, reg, tc.reader); !errors.Is(err, report.ErrContextUnavailable) {
				t.Fatalf("must fail closed, got %v", err)
			}
		})
	}
	rep.External = true
	if _, err := report.ResolveContextParams(ctx, rep, input, reg, reader); err == nil {
		t.Fatal("external report obtained infrastructure context")
	}
}

func TestReportContextYAML(t *testing.T) {
	for _, source := range []string{`{user: login}`, `{user_card: Staff.Department}`, `{user_card: {catalog: Staff, account_field: Account, field: Department}}`} {
		r, err := report.ParseBytes([]byte("name: Context\nparams:\n  - name: Actor\n    type: string\n    context: " + source + "\n"))
		if err != nil || r.Params[0].Context == nil {
			t.Fatalf("valid context %s: %v", source, err)
		}
	}
	for _, source := range []string{`null`, `{}`, `{user: null}`, `{user: login, user_card: Staff.Department}`, `{user: login, user: id}`, `{clinet: login}`, `{user_card: null}`} {
		if _, err := report.ParseBytes([]byte("name: Context\nparams:\n  - name: Actor\n    type: string\n    context: " + source + "\n")); err == nil {
			t.Fatalf("invalid context accepted: %s", source)
		}
	}
}
