package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Keep the submitting document alive with an iframe target while its actual
// native POST is blocked. Run the production idle scheduler with a deterministic
// clock so background throttling cannot conceal a duplicate autosave.
func TestFormIdleBrowserNativeSaveDoesNotDuplicate(t *testing.T) {
	browserPath := managedLayoutTestBrowser(t)
	s, entity := setupManagedEventsServer(t, `
Процедура ПриОткрытии()
	ПодключитьОбработчикОжидания("АвтоЗапись", 60);
КонецПроцедуры
Процедура АвтоЗапись()
	Объект.Записать();
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventOnOpen: "ПриОткрытии"}, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
	})
	s = New(s.reg, s.store, interpreter.New(), nil, Config{}, nil)
	t.Cleanup(s.Close)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{Login: "admin", IsAdmin: true})))
		})
	})
	s.MountStatic(router)
	s.Mount(router)
	formPath := "/ui/catalog/" + url.PathEscape(entity.Name) + "/new"
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var started, saved atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Logf("browser POST %s", r.URL.EscapedPath())
		}
		switch r.URL.Path {
		case "/__idle_native_state":
			_ = json.NewEncoder(w).Encode(map[string]bool{"started": started.Load(), "saved": saved.Load()})
			return
		case "/__idle_native_release":
			unblock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.EscapedPath() == formPath && r.Method == http.MethodPost {
			started.Store(true)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			router.ServeHTTP(w, r)
			saved.Store(true)
			return
		}
		if r.URL.EscapedPath() == formPath && r.Method == http.MethodGet {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, r)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(rec.Code)
			_, _ = w.Write([]byte(strings.Replace(rec.Body.String(), "</body>", formIdleNativeSaveScript+"</body>", 1)))
			return
		}
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { unblock(); s.CloseEventStreams(); server.Close() })
	browser := newMobileLayoutBrowser(t, browserPath)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	data, err := browser.measure(ctx, server.URL+formPath)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		IdlePosts int `json:"idlePosts"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	count, err := s.store.CountList(ctx, entity.Name, entity, storage.ListParams{})
	if err != nil || !started.Load() || !saved.Load() || result.IdlePosts != 0 || count != 1 {
		t.Fatalf("native saved=%v, idle=%d, rows=%d, err=%v", saved.Load(), result.IdlePosts, count, err)
	}
}

const formIdleNativeSaveScript = `<script>
window.addEventListener('load', async function() {
  const delay = () => new Promise(resolve => setTimeout(resolve, 10));
  const report = (id, text) => { const p = document.createElement('pre'); p.id = id; p.textContent = text; document.body.appendChild(p); };
  const state = async () => (await fetch('/__idle_native_state')).json();
  const wait = async (predicate, label) => {
    for (let i = 0; i < 500; i++) { if (await predicate()) return; await delay(); }
    throw new Error('native-save regression timed out: ' + label + '; state=' + JSON.stringify(await state()));
  };
  try {
    await wait(() => window.obFire && !window.obManagedIdleBusy(), 'initial form');
    let poll;
    const apply = window.obManagedApplyIdle;
    window.obManagedApplyIdle = commands => { poll = (commands || []).find(c => c.token) || poll; apply(commands); };
    await window.obFire('', 'ПриОткрытии');
    if (!poll || !poll.token) throw new Error('missing real signed idle command');
    window.obManagedApplyIdle([{proc: poll.proc, off: true}]);
    const frame = document.createElement('iframe'); frame.name = 'native-save-result'; document.body.appendChild(frame);
    const form = document.getElementById('main-form'); form.target = frame.name;
    form.querySelector('[name="Наименование"]').value = 'native save';
    const button = document.createElement('button'); button.type = 'submit'; form.appendChild(button);
    let now = 0, tick, pending, idlePosts = 0;
    const manager = obManagedIdleManager({
      now: () => now, hidden: () => false, modal: () => false,
      busy: () => window.obManagedIdleBusy(),
      fire: () => { idlePosts++; pending = window.obFire('', 'ОбработкаОжидания', {_idle_proc: poll.proc, _idle_token: poll.token}); return pending; },
      setTimeout: fn => { tick = fn; return 1; }, clearTimeout: () => {}
    });
    manager.apply([{proc: poll.proc, interval: 1, token: poll.token}]);
    await wait(() => !window.obManagedIdleBusy(), 'open event completion');
    if (!form.checkValidity()) throw new Error('invalid fixture form');
    let submitted;
    form.addEventListener('submit', event => { submitted = event; });
    form.requestSubmit(button);
    await Promise.resolve();
    if (!submitted || submitted.defaultPrevented) throw new Error('fixture native submit was cancelled');
    await wait(async () => (await state()).started, 'native POST started');
    now = 1000; tick();
    if (pending) await pending;
    await fetch('/__idle_native_release');
    await wait(async () => (await state()).saved, 'native POST completed');
    report('list-mobile-measure', JSON.stringify({idlePosts}));
  } catch (error) {
    await fetch('/__idle_native_release');
    report('list-mobile-error', String(error.stack || error));
  }
});
</script>`

// Use the public browser event entry, editor lock and shipped SlickGrid on a
// mounted HTTP form. Delay the signed tick's response while the user edits.
func TestFormIdleBrowserKeepsConcurrentTableEdits(t *testing.T) {
	browserPath := managedLayoutTestBrowser(t)
	s, entity, id := tpColumnsFixture(t, &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки",
	})
	form := entity.Forms[0]
	form.Attributes = []*metadata.FormAttribute{{Name: "Статус", TypeRef: "string"}}
	form.Elements = append(form.Elements, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "Статус", DataPath: "Статус",
	})
	form.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnOpen: "ПриОткрытии"}
	form.ProgramAST = mustParse(t, `
Процедура ПриОткрытии()
	ПодключитьОбработчикОжидания("Опрос", 60);
КонецПроцедуры
Процедура Опрос()
	Объект.Статус = "ответ обработчика";
	Для Каждого Стр Из Объект.Строки Цикл
		Стр.Цена = 99;
	КонецЦикла;
КонецПроцедуры
`)
	s = New(s.reg, s.store, interpreter.New(), nil, Config{}, nil)
	t.Cleanup(s.Close)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{Login: "admin", IsAdmin: true})))
		})
	})
	s.MountStatic(router)
	s.Mount(router)
	formPath := "/ui/document/" + url.PathEscape(entity.Name) + "/" + id.String()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != formPath {
			router.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(rec.Code)
		_, _ = w.Write([]byte(strings.Replace(rec.Body.String(), "</body>", formIdleConcurrentScript+"</body>", 1)))
	}))
	t.Cleanup(func() { s.CloseEventStreams(); server.Close() })
	browser := newMobileLayoutBrowser(t, browserPath)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	data, err := browser.measure(ctx, server.URL+formPath)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Cases int `json:"cases"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Cases != 4 {
		t.Fatalf("incomplete actual browser regression: %s (%v)", data, err)
	}
	t.Logf("signed HTTP ticks; active editor, insertion, deletion and untouched table: %s", data)
}

const formIdleConcurrentScript = `<script>
window.addEventListener('load', async function() {
  const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
  const check = (ok, message) => { if (!ok) throw new Error(message); };
  const wait = async (predicate, label) => {
    for (let i = 0; i < 500; i++) { if (predicate()) return; await delay(10); }
    throw new Error('browser regression timed out: ' + label + '; visibility=' + document.visibilityState);
  };
  const report = (id, text) => {
    const out = document.createElement('pre'); out.id = id; out.textContent = text; document.body.appendChild(out);
  };
  try {
    await wait(() => (window._obGridViews || []).length === 1 && !window.obManagedIdleBusy(), 'grid and form-event queue');
    let poll;
    const applyIdle = window.obManagedApplyIdle;
    window.obManagedApplyIdle = commands => {
      for (const command of commands || []) if (command.token) poll = command;
      applyIdle(commands);
    };
    await window.obFire('', 'ПриОткрытии');
    check(poll && poll.token, 'HTTP open did not sign a timer');
    window.obManagedApplyIdle([{proc: poll.proc, off: true}]);
    const view = window._obGridViews[0];
    const rows = () => view.grid.getData().getItems();
    const realFetch = window.fetch;
    let release, received;
    window.fetch = async function(target, options) {
      const response = await realFetch(target, options);
      if (options && new URLSearchParams(options.body).get('_event') === 'ОбработкаОжидания') {
        received = await response.clone().json();
        return new Promise(resolve => { release = () => resolve(response); });
      }
      return response;
    };
    let cases = 0;
    for (const mode of ['editor', 'insert', 'delete', 'untouched']) {
      // Re-establish a deterministic starting table through the production renderer.
      window.applyTableParts({Строки: [{Количество: 2, Цена: 15, Сумма: 30}, {Количество: 3, Цена: 16, Сумма: 48}]});
      window.obGridSync();
      release = null; received = null;
      // The same public entry used by the timer; keep the timer clock out of
      // the regression so browser background throttling cannot hide a race.
      const pending = window.obFire('', 'ОбработкаОжидания', {_idle_proc: poll.proc, _idle_token: poll.token});
      await wait(() => release, mode + ' signed tick response');
      check(received.ok && !received.error, 'signed tick failed: ' + JSON.stringify(received));
      if (mode === 'editor') {
        const column = view.grid.getColumns().findIndex(column => column.id === 'Количество');
        view.grid.gotoCell(0, column, true);
        const editor = view.grid.getCellEditor();
        check(editor, 'real cell editor did not open');
        editor.setValue('7'); // Deliberately leave the active editor uncommitted.
      } else if (mode === 'insert') {
        view.grid.getData().addItem({id: 'user-row', _ord: 2, Количество: 8, Цена: 17, Сумма: 136});
        window.obSetManagedFormDirty(true);
      } else if (mode === 'delete') {
        view.grid.getData().deleteItem(rows()[1].id);
        window.obSetManagedFormDirty(true);
      }
      release();
      await pending;
      check(document.querySelector('[name="Статус"]').value === 'ответ обработчика', mode + ': untouched field lost server update');
      if (mode === 'editor') check(Number(rows()[0].Количество) === 7, 'idle response lost active cell edit');
      if (mode === 'insert') check(rows().length === 3 && Number(rows()[2].Количество) === 8, 'idle response lost inserted row');
      if (mode === 'delete') check(rows().length === 1, 'idle response restored deleted row');
      if (mode === 'untouched') check(Number(rows()[0].Цена) === 99, 'untouched table lost handler update');
      if (mode !== 'untouched') check(window._obFormDirty, 'newer edit lost dirty state');
      cases++;
    }
    window.obManagedApplyIdle([{proc: poll.proc, off: true}]);
    report('list-mobile-measure', JSON.stringify({cases}));
  } catch (error) { report('list-mobile-error', String(error.stack || error)); }
});
</script>`
