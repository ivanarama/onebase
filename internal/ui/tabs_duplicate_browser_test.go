package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/stretchr/testify/require"
)

func TestTabsDuplicateControlThroughPublicPages(t *testing.T) {
	executable := managedLayoutTestBrowser(t)
	doc := &metadata.Entity{Name: "TabOrder", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Title", Type: metadata.FieldTypeString}}}
	catalog := &metadata.Entity{Name: "TabClient", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Title", Type: metadata.FieldTypeString}}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc, catalog})
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc, catalog}, Reports: []*report.Report{
		{Name: "TabReport", Title: "Проверка отчёта", Query: "ВЫБРАТЬ 1 КАК Число"},
	}})
	id := uuid.New()
	require.NoError(t, s.store.Upsert(ctx, doc.Name, id, map[string]any{"Title": "Документ"}, doc))
	clientID := uuid.New()
	require.NoError(t, s.store.Upsert(ctx, catalog.Name, clientID, map[string]any{"Title": "Клиент"}, catalog))
	router := chi.NewRouter()
	s.MountStatic(router)
	s.Mount(router)
	server := httptest.NewServer(router)
	t.Cleanup(func() { s.CloseEventStreams(); server.Close() })

	paths := map[string]string{
		"list": "/ui/document/taborder", "newDoc": "/ui/document/taborder/new",
		"doc": "/ui/document/taborder/" + id.String(), "newClient": "/ui/catalog/tabclient/new",
		"client": "/ui/catalog/tabclient/" + clientID.String(), "report": "/ui/report/TabReport",
	}
	for key, path := range paths {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code, key)
	}
	pathsJSON, err := json.Marshal(paths)
	require.NoError(t, err)

	browser := newMobileLayoutBrowser(t, executable)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var page struct {
		TargetID string `json:"targetId"`
	}
	require.NoError(t, browser.call(ctx, "", "Target.createTarget", map[string]any{"url": server.URL + "/ui/app"}, &page))
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	require.NoError(t, browser.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": page.TargetID, "flatten": true}, &attached))

	eval := func(script string) (value string, details json.RawMessage) {
		t.Helper()
		var out struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
			ExceptionDetails json.RawMessage `json:"exceptionDetails"`
		}
		err := browser.call(ctx, attached.SessionID, "Runtime.evaluate", map[string]any{
			"expression": script, "awaitPromise": true, "returnByValue": true,
		}, &out)
		require.NoError(t, err)
		return out.Result.Value, out.ExceptionDetails
	}
	for {
		ready, details := eval(`typeof window.obOpenTab === 'function' ? 'ready' : 'loading'`)
		require.Empty(t, details)
		if ready == "ready" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("оболочка вкладок не загрузилась")
		case <-time.After(30 * time.Millisecond):
		}
	}
	got, details := eval(`(async (paths) => {
		const label = 'Открыть ещё один экземпляр';
		const strip = document.querySelector('#ob-tabstrip');
		function require(ok, message) { if (!ok) throw new Error(message); }
		function loaded(tab, path) {
			return new Promise(resolve => tab.frame.addEventListener('load', function ready() {
				if (tab.frame.contentWindow.location.pathname !== path) return;
				tab.frame.removeEventListener('load', ready);
				resolve();
			}));
		}
		async function open(path) {
			const tab = window.obOpenTab(path, path);
			await loaded(tab, path);
			return tab;
		}
		function check(tab, expect) {
			require(!!tab.btn.querySelector('.ob-tab-dup') === expect, 'icon: ' + tab.url);
			tab.btn.dispatchEvent(new MouseEvent('contextmenu', {bubbles: true, cancelable: true}));
			const menu = document.querySelector('#ob-tabmenu');
			const action = [...menu.children].find(item => item.textContent === label);
			require(!!action === expect, 'context menu: ' + tab.url);
			return action;
		}
		const list = await open(paths.list);
		check(list, true);
		const inFrame = loaded(list, paths.doc);
		list.frame.contentWindow.location.assign(paths.doc);
		await inFrame;
		require(!document.querySelector('#ob-tabmenu'), 'stale list context menu');
		check(list, false);
		check(await open(paths.newDoc), false);
		check(await open(paths.client), false);
		check(await open(paths.newClient), false);
		const report = await open(paths.report);
		const duplicate = check(report, true);
		const before = strip.children.length;
		duplicate.click();
		require(strip.children.length === before + 1, 'report did not duplicate from context menu');
		report.btn.querySelector('.ob-tab-dup').click();
		require(strip.children.length === before + 2, 'report did not duplicate from icon');
		return 'ok';
	})(` + string(pathsJSON) + `)`)
	require.Empty(t, details, string(details))
	require.Equal(t, "ok", got)
}
