package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	reportpkg "github.com/ivantit66/onebase/internal/report"
)

// Exercise shipped assets in the public report, using trusted browser keyboard
// and real clipboard paste. Completing an edit with Enter must not run the report.
func TestDateMaskBrowserReportEditing(t *testing.T) {
	browserPath := managedLayoutTestBrowser(t)
	rep := &reportpkg.Report{Name: "DateMaskProbe", Query: "ВЫБРАТЬ 1 КАК Marker", Params: []reportpkg.Param{
		{Name: "Day", Type: "date"}, {Name: "Moment", Type: "datetime"},
	}}
	fixture := newReportExportTestServer(t, rep)
	s := New(fixture.reg, fixture.store, interpreter.New(), nil, Config{ShortDateInput: true}, nil)
	t.Cleanup(s.Close)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{
				Login: "date-mask-operator",
				Roles: []*auth.Role{{Name: "ОператорКЦ", Permissions: auth.Permission{
					Reports: map[string][]string{rep.Name: {"run"}},
				}}},
			})))
		})
	})
	s.MountStatic(router)
	s.Mount(router)
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/ui/report/DateMaskProbe" {
			posts.Add(1)
		}
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { s.CloseEventStreams(); server.Close() })
	b := newMobileLayoutBrowser(t, browserPath)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	var isolated struct {
		ID string `json:"browserContextId"`
	}
	if err := b.call(ctx, "", "Target.createBrowserContext", nil, &isolated); err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		if err := b.call(closeCtx, "", "Target.disposeBrowserContext", map[string]any{"browserContextId": isolated.ID}, nil); err != nil {
			t.Error(err)
		}
	}()
	var page struct {
		ID string `json:"targetId"`
	}
	if err := b.call(ctx, "", "Target.createTarget", map[string]any{"url": server.URL + "/ui/report/DateMaskProbe", "browserContextId": isolated.ID}, &page); err != nil {
		t.Fatal(err)
	}
	var attached struct {
		ID string `json:"sessionId"`
	}
	if err := b.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": page.ID, "flatten": true}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := b.call(ctx, attached.ID, "Page.bringToFront", nil, nil); err != nil {
		t.Fatal(err)
	}
	eval := func(expression string) json.RawMessage {
		t.Helper()
		var out struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
			Exception json.RawMessage `json:"exceptionDetails"`
		}
		if err := b.call(ctx, attached.ID, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true}, &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Exception) != 0 {
			t.Fatal("browser measurement script failed")
		}
		return out.Result.Value
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for string(eval(`Boolean(document.querySelector('input[name="Day"]') && window.obCommitDateInput)`)) != "true" {
		select {
		case <-ctx.Done():
			t.Fatal("public report assets did not initialize")
		case <-tick.C:
		}
	}
	key := func(name string, code int, text string, modifiers int) {
		t.Helper()
		args := map[string]any{"type": "keyDown", "key": name, "windowsVirtualKeyCode": code, "modifiers": modifiers}
		if text != "" {
			args["text"] = text
		}
		if err := b.call(ctx, attached.ID, "Input.dispatchKeyEvent", args, nil); err != nil {
			t.Fatal(err)
		}
		args["type"] = "keyUp"
		delete(args, "text")
		if err := b.call(ctx, attached.ID, "Input.dispatchKeyEvent", args, nil); err != nil {
			t.Fatal(err)
		}
	}
	typeDigits := func(value string) {
		for _, digit := range value {
			key(string(digit), int(digit), string(digit), 0)
		}
	}
	assertJS := func(expression, message string) {
		t.Helper()
		if string(eval("Boolean("+expression+")")) != "true" {
			t.Fatal(message)
		}
	}
	eval(`window.maskTrusted=0; document.addEventListener('keydown',e=>{if(e.isTrusted && /\d/.test(e.key))window.maskTrusted++;},true); document.querySelector('input[name="Day"]').focus()`)
	assertJS(`document.activeElement.value==='__.__.____'`, "missing date template")
	typeDigits("16")
	assertJS(`document.activeElement.value==='16.__.____' && document.activeElement.selectionStart===3`, "date cursor did not skip the dot")
	key("Backspace", 8, "", 0)
	assertJS(`document.activeElement.value==='1_.__.____' && document.activeElement.selectionStart===1`, "Backspace damaged the mask")
	typeDigits("6112026")
	key("Enter", 13, "\r", 0)
	assertJS(`document.querySelector('input[name="Day"]').value==='2026-11-16'`, "keyboard date did not serialize as ISO")
	eval(`document.querySelector('input[name="Moment"]').focus()`)
	typeDigits("16112026235958")
	key("Enter", 13, "\r", 0)
	assertJS(`document.querySelector('input[name="Moment"]').value==='2026-11-16T23:59:58'`, "date-time seconds lost")
	eval(`document.querySelector('input[name="Day"]').focus()`)
	key("a", 65, "", 2)
	typeDigits("1611")
	key("Enter", 13, "\r", 0)
	assertJS(`document.querySelector('input[name="Day"]').value===String(new Date().getFullYear())+'-11-16'`, "partial date did not retain day and month and complete the year")
	eval(`document.querySelector('input[name="Moment"]').focus()`)
	key("a", 65, "", 2)
	typeDigits("7")
	assertJS(`(() => {
		const now=new Date(), prefix=now.getFullYear()+'-'+String(now.getMonth()+1).padStart(2,'0')+'-07T00:00';
		return new FormData(document.querySelector('input[name="Moment"]').form).get('Moment')===prefix+':00';
	})()`, "focused short date-time did not serialize with default components")
	key("Tab", 9, "", 0)
	assertJS(`(() => {
		const now=new Date(), prefix=now.getFullYear()+'-'+String(now.getMonth()+1).padStart(2,'0')+'-07T00:00';
		return document.querySelector('input[name="Moment"]').value.startsWith(prefix);
	})()`, "Tab did not complete date-time to midnight")
	eval(`document.querySelector('input[name="Moment"]').focus()`)
	key("a", 65, "", 2)
	typeDigits("16.11.2026 8::30")
	assertJS(`new FormData(document.querySelector('input[name="Moment"]').form).get('Moment')==='2026-11-16T08:00:30'`, "omitted minutes shifted the seconds in the request")
	key("Enter", 13, "\r", 0)
	assertJS(`document.querySelector('input[name="Moment"]').value==='2026-11-16T08:00:30'`, "Enter did not preserve the entered seconds")
	if err := b.call(ctx, "", "Browser.grantPermissions", map[string]any{"origin": server.URL, "browserContextId": isolated.ID, "permissions": []string{"clipboardReadWrite", "clipboardSanitizedWrite"}}, nil); err != nil {
		t.Fatal(err)
	}
	// Keep the previous clipboard only in this isolated page and restore it;
	// neither test output nor assertions ever include its contents.
	eval(`(async()=>{window.maskSavedClipboard=await navigator.clipboard.readText();return true})()`)
	defer func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer restoreCancel()
		if err := b.call(restoreCtx, attached.ID, "Runtime.evaluate", map[string]any{"expression": `navigator.clipboard.writeText(window.maskSavedClipboard)`, "awaitPromise": true}, nil); err != nil {
			t.Error("restore owned browser clipboard operation:", err)
		}
	}()
	for _, text := range []string{"16.11.2026", "16112026"} {
		raw, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		eval(fmt.Sprintf(`(async()=>{await navigator.clipboard.writeText(%s);document.querySelector('input[name="Day"]').focus();return true})()`, raw))
		key("a", 65, "", 2)
		key("Delete", 46, "", 0)
		key("v", 86, "", 2)
		for string(eval(`document.querySelector('input[name="Day"]').value==='16.11.2026'`)) != "true" {
			select {
			case <-ctx.Done():
				t.Fatal("browser clipboard paste did not fill the date mask")
			case <-tick.C:
			}
		}
		key("Enter", 13, "\r", 0)
		assertJS(`document.querySelector('input[name="Day"]').value==='2026-11-16'`, "clipboard date did not serialize as ISO")
	}
	assertJS(`window.maskTrusted>=20`, "keyboard events were not trusted browser input")
	if got := posts.Load(); got != 0 {
		t.Fatalf("date editing submitted the report %d times", got)
	}
}
