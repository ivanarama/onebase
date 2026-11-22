package ui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// messages: popup / message_seconds в config/home_page.yaml доходят до
// страницы меткой ob-messages; без настройки метки нет — поведение прежнее.
func TestMessagesMode_PageMeta(t *testing.T) {
	for _, tc := range []struct {
		hp   *metadata.HomePage
		want string
	}{
		{&metadata.HomePage{Messages: metadata.MessagesPopup, MessageSeconds: 10}, `<meta name="ob-messages" content="popup" data-seconds="10">`},
		{&metadata.HomePage{MessageSeconds: 8}, `<meta name="ob-messages" content="panel" data-seconds="8">`},
		{&metadata.HomePage{}, ""},
	} {
		s, _ := newSubmitTestServer(t, nil)
		s.reg.LoadHomePage(tc.hp)
		r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
		r = r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{Login: "op"}))
		w := httptest.NewRecorder()
		s.index(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /ui/: %d", w.Code)
		}
		body := w.Body.String()
		if tc.want == "" {
			if strings.Contains(body, `name="ob-messages"`) {
				t.Fatalf("без настройки метки быть не должно")
			}
			continue
		}
		if !strings.Contains(body, tc.want) {
			t.Fatalf("нет %s в странице", tc.want)
		}
	}
}

func TestMessagesMode_LoaderValidates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "home_page.yaml")
	if err := os.WriteFile(path, []byte("messages: toast\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := metadata.LoadHomePage(path); err == nil {
		t.Fatal("неизвестный режим messages должен быть отклонён")
	}
	if err := os.WriteFile(path, []byte("messages: popup\nmessage_seconds: 10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hp, err := metadata.LoadHomePage(path)
	if err != nil || hp.Messages != "popup" || hp.MessageSecondsOrDefault() != 10 {
		t.Fatalf("hp=%+v err=%v", hp, err)
	}
}

func TestMessagesPopupBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required")
	}
	out, err := exec.Command(node, "--test", "static/messages_popup_behavior_test.js").CombinedOutput() //nolint:gosec // test-only executable resolved by exec.LookPath
	if err != nil {
		t.Fatalf("node messages popup test: %v\n%s", err, out)
	}
}
