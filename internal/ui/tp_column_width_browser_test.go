package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Exercise the mounted document and shipped SlickGrid, including two editable
// placements of the same table part. A stub of autosizeColumns cannot detect a
// lost width, editor or placement-specific column definition in the real grid.
func TestTPColumnWidthsBrowserPlacementsAndEditors(t *testing.T) {
	browserPath := managedLayoutTestBrowser(t)
	makeTable := func(name string, noGrid bool, widths []int) *metadata.FormElement {
		table := &metadata.FormElement{
			Kind: metadata.FormElementTablePart, Name: name,
			DataPath: "Объект.Строки", NoGrid: noGrid,
		}
		for i, field := range []string{"Количество", "Цена", "Сумма"} {
			column := tpColumn(name+field, field)
			column.Width = widths[i]
			table.Children = append(table.Children, column)
		}
		return table
	}
	s, entity, id := tpColumnsFixture(t, &metadata.FormElement{
		Kind: metadata.FormElementGroupBox, Name: "Таблицы",
		Children: []*metadata.FormElement{
			makeTable("Первая", false, []int{90, 140, 0}),
			makeTable("Вторая", false, []int{170, 0, 110}),
			makeTable("Простая", true, []int{90, 140, 0}),
		},
	})
	s = New(s.reg, s.store, interpreter.New(), nil, Config{}, nil)
	t.Cleanup(s.Close)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.ContextWithUser(r.Context(), &auth.User{Login: "admin", IsAdmin: true})
			next.ServeHTTP(w, r.WithContext(ctx))
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
		if rec.Code != http.StatusOK {
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.Replace(rec.Body.String(), "</body>", tpColumnWidthMeasureScript+"</body>", 1)))
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
		Phases int `json:"phases"`
		Edits  int `json:"edits"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("browser result %s: %v", data, err)
	}
	if result.Phases != 3 || result.Edits != 6 {
		t.Fatalf("incomplete browser matrix: %s", data)
	}
	t.Logf("mounted HTTP form, shipped assets, two placements, wide/narrow/wide resize and six numeric editors: %s", data)
}

const tpColumnWidthMeasureScript = `<script>
window.addEventListener('load', async function() {
  const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
  const check = (ok, message) => { if (!ok) throw new Error(message); };
  const report = (id, value) => {
    const out = document.createElement('pre'); out.id = id;
    out.textContent = value; document.body.appendChild(out);
  };
  try {
    for (let i = 0; i < 100 && (window._obGridViews || []).length !== 2; i++) await delay(20);
    const views = window._obGridViews || [];
    check(views.length === 2, 'both editable grid placements must initialize');
    const expected = [[90, 140, 0], [170, 0, 110]];
    const fields = ['Количество', 'Цена', 'Сумма'];
    let phases = 0, edits = 0;
    for (const available of [900, 260, 900]) {
      for (const view of views) {
        view.div.style.setProperty('width', available + 'px', 'important');
        view.div.style.setProperty('max-width', 'none', 'important');
        view.div.style.flex = 'none';
      }
      // Exercise the production resize listener, including its debounce.
      window.dispatchEvent(new Event('resize'));
      await delay(200);
      for (let v = 0; v < views.length; v++) {
        const view = views[v], columns = view.grid.getColumns();
        check(Math.abs(view.div.clientWidth - available) <= 2, 'fixture container width: ' + view.div.clientWidth + ' vs ' + available);
        for (let c = 0; c < fields.length; c++) {
          const index = columns.findIndex(column => column.id === fields[c]);
          check(index >= 0, 'missing column ' + fields[c]);
          const column = columns[index], width = expected[v][c];
          check(typeof column.editor === 'function', 'numeric editor lost');
          const header = view.grid.getHeaderColumn(column.id);
          check(header && Math.abs(header.getBoundingClientRect().width - column.width) <= 1,
                'header geometry disagrees with SlickGrid column');
          if (width) check(column.width === width, 'declared width changed in placement ' + v + ': ' + fields[c]);
          else if (available === 900) check(column.width > 120, 'free column did not receive remaining space');
          else check(column.width === 120, 'free column lost its narrow-container default');
        }
      }
      phases++;
    }
    // Each placement keeps its own widths while retaining real numeric editors.
    // Commit through SlickGrid's editor lock and check the shared row afterwards.
    for (const view of views) {
      for (let c = 0; c < fields.length; c++) {
        const columns = view.grid.getColumns();
        const index = columns.findIndex(column => column.id === fields[c]);
        const before = columns.map(column => column.width);
        view.grid.gotoCell(0, index, true);
        const editor = view.grid.getCellEditor();
        check(editor, 'cell did not enter edit mode');
        editor.setValue('7,5');
        check(view.grid.getEditorLock().commitCurrentEdit(), 'numeric edit rejected');
        check(Number(view.grid.getDataItem(0)[fields[c]]) === 7.5, 'numeric edit lost');
        check(view.grid.getColumns().every((column, i) => column.width === before[i]), 'editing changed widths');
        edits++;
      }
    }
    const simple = document.querySelector('table[data-tp="Строки"]');
    check(simple, 'simple table missing');
    const headers = Array.from(simple.querySelectorAll('thead th'));
    for (let c = 0; c < fields.length; c++) {
      const header = headers.find(node => node.textContent.trim() === fields[c]);
      check(header, 'simple header missing');
      check(header.style.width === (expected[0][c] ? expected[0][c] + 'px' : ''), 'simple column width lost');
      check(simple.querySelector('[name="tp.Строки.0.' + fields[c] + '"]'), 'simple numeric input lost');
    }
    report('list-mobile-measure', JSON.stringify({phases, edits}));
  } catch (error) { report('list-mobile-error', error.stack || String(error)); }
});
</script>`
