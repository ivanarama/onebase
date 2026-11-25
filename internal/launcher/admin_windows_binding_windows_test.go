package launcher

import (
	"errors"
	"net/http"
	"os/user"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"golang.org/x/net/html"
)

func TestCfgAdminWindowsAccount(t *testing.T) {
	ctx := t.Context()
	f := newDomainConfigurator(t, "windows-binding")
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	checkCard := func(want string) {
		t.Helper()
		page := f.request(t, "users", f.adminToken, nil, http.StatusOK)
		doc, err := html.Parse(strings.NewReader(page))
		if err != nil {
			t.Fatal(err)
		}
		for node := range doc.Descendants() {
			if id, _ := attr(node, "data-user-id"); node.Data == "button" && id == f.operator.ID {
				if account, ok := attr(node, "data-account"); ok && account == want {
					return
				}
			}
		}
		t.Fatalf("user list does not offer to edit the operator's binding %q", want)
	}
	checkCard("")
	const binding = "users/windows-account"
	f.request(t, binding, f.adminToken, map[string]any{"id": f.operator.ID, "enabled": true, "account": account.Username}, http.StatusOK)
	bound, err := f.repo.GetByWindowsSID(ctx, account.Uid)
	if err != nil || bound.ID != f.operator.ID || !strings.EqualFold(bound.WindowsAccount, account.Username) {
		t.Fatalf("wrong Windows binding: user=%v err=%v", bound, err)
	}
	checkCard(bound.WindowsAccount)

	clear := map[string]any{"id": f.operator.ID, "enabled": false}
	f.request(t, binding, f.ordinaryToken, clear, http.StatusFound)
	for _, invalid := range []map[string]any{
		{"id": f.operator.ID},
		{"id": f.operator.ID, "enabled": true, "account": "  "},
		{"id": f.operator.ID, "enabled": true, "account": "unqualified"},
	} {
		f.request(t, binding, f.adminToken, invalid, http.StatusBadRequest)
	}
	meta := auth.SessionMeta{Kind: auth.SessionKindEnterprise}
	if _, err := f.repo.CreateWindowsSession(ctx, f.operator.ID, account.Uid, meta); err != nil {
		t.Fatalf("Enterprise lost the binding after a rejected edit: %v", err)
	}
	f.request(t, binding, f.adminToken, clear, http.StatusOK)
	checkCard("")
	if _, err := f.repo.CreateWindowsSession(ctx, f.operator.ID, account.Uid, meta); !errors.Is(err, auth.ErrAccountUnavailable) {
		t.Fatalf("Enterprise accepted a cleared SID or returned an unexpected error: %v", err)
	}
}
