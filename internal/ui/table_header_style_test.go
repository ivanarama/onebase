package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Шапка таблиц выделена фоном, тёмным полужирным текстом и заметной линией —
// одинаково в списках, табличных частях, подборе, виджетах и отчётах. Раньше
// светло-серый текст на белом фоне сливался с данными.
func TestListPage_TableHeaderStandsOut(t *testing.T) {
	ent := &metadata.Entity{Name: "ШапкаКлиент", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	req := httptest.NewRequest("GET", "/ui/catalog/шапкаклиент", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", "шапкаклиент")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.list(rec, req)
	if rec.Code != 200 {
		t.Fatalf("список: code = %d, body: %s", rec.Code, rec.Body.String())
	}
	checkHeaderRules(t, "список", rec.Body.String(), `\.tbl th`, `\.tp-table th`, `\.report-composed thead th`, "\nth")

	// Виджеты-таблицы рабочего стола («Список задач») — стили главной.
	home := httptest.NewRecorder()
	s.index(home, httptest.NewRequest("GET", "/ui/", nil).WithContext(ctx))
	if home.Code != 200 {
		t.Fatalf("главная: code = %d", home.Code)
	}
	checkHeaderRules(t, "главная", home.Body.String(), `\.w-list th`)
}

// Measure the actual sortable text on a populated, mounted HTTP list. Checking
// only th misses a lighter color set directly on its child link.
func TestListPage_SortableHeaderContrastBrowser(t *testing.T) {
	browserPath := managedLayoutTestBrowser(t)
	ent := &metadata.Entity{Name: "ШапкаКлиент", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	if err := s.store.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": "Клиент шапки"}, ent); err != nil {
		t.Fatal(err)
	}
	s = New(s.reg, s.store, s.interp, nil, Config{}, nil)
	t.Cleanup(s.Close)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{Login: "admin", IsAdmin: true})))
		})
	})
	s.MountStatic(router)
	s.Mount(router)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/catalog/шапкаклиент" {
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
		_, _ = w.Write([]byte(strings.Replace(rec.Body.String(), "</body>", sortableHeaderMeasureScript+"</body>", 1)))
	}))
	t.Cleanup(func() { s.CloseEventStreams(); server.Close() })
	browser := newMobileLayoutBrowser(t, browserPath)
	measureCtx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	data, err := browser.measure(measureCtx, server.URL+"/ui/catalog/шапкаклиент")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Links    int     `json:"links"`
		Contrast float64 `json:"contrast"`
		Color    string  `json:"color"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Links == 0 || got.Contrast < 4.5 || got.Color != "rgb(51, 65, 85)" {
		t.Fatalf("sortable header must keep dark text and AA contrast: %s", data)
	}
	t.Logf("populated HTTP list, computed sortable header color/contrast: %s", data)
}

const sortableHeaderMeasureScript = `<script>
window.addEventListener('load', () => {
  const report = (id, value) => {
    const out = document.createElement('pre'); out.id = id;
    out.textContent = value; document.body.appendChild(out);
  };
  try {
    const links = [...document.querySelectorAll('table thead th a[href*="sort="]')];
    if (!links.length || !document.querySelector('#list-body tr')) throw new Error('missing populated sortable list');
    const luminance = color => {
      const rgb = color.match(/[\d.]+/g).slice(0, 3).map(Number).map(v => {
        v /= 255; return v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
      });
      return rgb[0] * 0.2126 + rgb[1] * 0.7152 + rgb[2] * 0.0722;
    };
    const contrasts = links.map(link => {
      const style = getComputedStyle(link);
      if (style.color !== 'rgb(51, 65, 85)') throw new Error('light sortable text: ' + style.color);
      const background = getComputedStyle(link.closest('th')).backgroundColor;
      if (background !== 'rgb(238, 242, 247)') throw new Error('unexpected header background: ' + background);
      const fg = luminance(style.color), bg = luminance(background);
      return (Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05);
    });
    report('list-mobile-measure', JSON.stringify({links: links.length, contrast: Math.min(...contrasts), color: getComputedStyle(links[0]).color}));
  } catch (err) { report('list-mobile-error', String(err)); }
});
</script>`

func checkHeaderRules(t *testing.T, page, body string, selectors ...string) {
	t.Helper()
	for _, sel := range selectors {
		rule := regexp.MustCompile(sel + `\{[^}]*\}`).FindString(body)
		if rule == "" {
			t.Errorf("%s: в стилях нет правила %s", page, sel)
			continue
		}
		for _, want := range []string{"background:#eef2f7", "color:#334155", "font-weight:600", "border-bottom:2px solid #cbd5e1"} {
			if !strings.Contains(rule, want) {
				t.Errorf("%s, шапка %s: нет %s в %q", page, sel, want, strings.TrimSpace(rule))
			}
		}
	}
}
