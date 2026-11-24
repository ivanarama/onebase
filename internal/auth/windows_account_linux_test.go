package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveWinbindAccount(t *testing.T) {
	const domainSID = "S-1-5-21-111-222-333"
	const sid = domainSID + "-1001"
	const info = "Name              : EXAMPLE\nAlt_Name          : example.test\nSID               : " + domainSID +
		"\nActive Directory  : Yes\nNative            : Yes\nPrimary           : Yes"
	const result = sid + " SID_USER (1)"
	const account = `example.test\Иван Петров`
	for _, tt := range []struct {
		name   string
		info   string
		result string
		wantOK bool
	}{
		{"domain user", info, result, true},
		{"group", info, sid + " SID_DOM_GROUP (2)", false},
		{"computer", info, sid + " SID_COMPUTER (9)", false},
		{"unix user", info, "S-1-22-1-1001 SID_USER (1)", false},
		{"local SAM", strings.Replace(info, "Active Directory  : Yes", "Active Directory  : No", 1), result, false},
		{"different directory domain", strings.ReplaceAll(info, "example.test", "other.test"), result, false},
		{"different SID domain", info, "S-1-5-21-333-222-111-1001 SID_USER (1)", false},
		{"truncated directory SID", strings.Replace(info, domainSID, "S-1-5-21", 1), result, false},
		{"well-known SID", info, "S-1-5-18 SID_USER (1)", false},
		{"invalid RID", info, domainSID + "-4294967296 SID_USER (1)", false},
		{"noncanonical SID", info, domainSID + "-01001 SID_USER (1)", false},
		{"missing type", info, sid, false},
		{"unexpected additional result", info, result + "\n" + result, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wbinfo := func(option string) (string, error) {
				switch option {
				case "--separator":
					return "+", nil
				case "--domain-info=example.test":
					return tt.info, nil
				case "--name-to-sid=EXAMPLE+Иван Петров":
					return tt.result, nil
				default:
					t.Fatalf("unexpected wbinfo option %q", option)
					return "", nil
				}
			}
			gotSID, display, err := resolveWinbindAccount(account, "EXAMPLE.TEST", wbinfo)
			if tt.wantOK {
				require.NoError(t, err)
				require.Equal(t, sid, gotSID)
				require.Equal(t, `EXAMPLE\Иван Петров`, display)
			} else {
				require.Error(t, err)
				require.Empty(t, gotSID)
				require.Empty(t, display)
			}
		})
	}
}

func TestResolveWinbindRejectsAmbiguousInputs(t *testing.T) {
	for _, name := range []string{
		"operator", `\operator`, `EXAMPLE\`, `EXAMPLE\x\y`, "EXAMPLE\\a\nb",
		strings.Repeat("a", 256) + `\operator`, `EXAMPLE\` + strings.Repeat("п", 128),
	} {
		_, _, err := resolveWinbindAccount(name, "EXAMPLE.TEST", func(string) (string, error) {
			t.Fatal("invalid input was sent to winbind")
			return "", nil
		})
		require.Error(t, err)
	}
	unavailable := errors.New("winbind unavailable")
	_, _, err := resolveWinbindAccount(`EXAMPLE\operator`, "EXAMPLE.TEST", func(string) (string, error) { return "", unavailable })
	require.ErrorIs(t, err, unavailable)
}

func TestWinbindBindingRequiresServiceRealm(t *testing.T) {
	_, _, err := resolveWinbindAccount(`TRUSTED\operator`, "EXAMPLE.TEST", func(option string) (string, error) {
		switch option {
		case "--separator":
			return `\`, nil
		case "--domain-info=TRUSTED":
			return "Name : TRUSTED\nAlt_Name : trusted.test\nActive Directory : Yes\nSID : S-1-5-21-1-2-3", nil
		default:
			t.Fatal("unexpected lookup outside the configured realm")
			return "", nil
		}
	})
	require.ErrorContains(t, err, "настроенного домена")
}
