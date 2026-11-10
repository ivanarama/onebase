package ui

import (
	"context"
	"reflect"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

func TestCurrentUserRolesAreRequestScopedSnapshot(t *testing.T) {
	s, ctx := newSubmitTestServer(t, nil)
	roles := []*auth.Role{{Name: "Chief"}, nil, {Name: "Reader"}}
	user := &auth.User{ID: "first", Roles: roles}
	read := func(c context.Context) []any {
		t.Helper()
		vars, _ := s.buildDSLVarsTx(c, nil)
		obj, err := vars["ТекущийПользователь"].(interpreter.BuiltinFunc)(nil, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		m := obj.(*interpreter.MapThis).M
		if !reflect.DeepEqual(m["Роли"], m["Roles"]) {
			t.Fatal("role aliases differ")
		}
		return m["Роли"].([]any)
	}
	first := read(auth.ContextWithUser(ctx, user))
	if !reflect.DeepEqual(first, []any{"Chief", "Reader"}) {
		t.Fatalf("roles: %#v", first)
	}
	first[0] = "Admin"
	if got := read(auth.ContextWithUser(ctx, user)); !reflect.DeepEqual(got, []any{"Chief", "Reader"}) {
		t.Fatalf("request changed authority: %#v", got)
	}
	if got := read(auth.ContextWithUser(ctx, &auth.User{ID: "second"})); len(got) != 0 {
		t.Fatalf("other user's roles leaked: %#v", got)
	}
	if got := read(ctx); len(got) != 0 {
		t.Fatalf("anonymous roles: %#v", got)
	}
}
