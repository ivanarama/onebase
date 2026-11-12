package ui

import (
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The mounted dashboard must fit five filters and Reset on one row by default,
// without compact density. A narrow card must wrap without overflowing.
func TestWidgetFiltersFiveControlsShareRowBrowser(t *testing.T) {
	browserPath := managedLayoutTestBrowser(t)
	e := &metadata.Entity{Name: "FilterLayout", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	fixture, ctx := newSubmitTestServer(t, []*metadata.Entity{e})
	if err := fixture.store.Upsert(ctx, e.Name, uuid.New(), map[string]any{"Наименование": "Synthetic row"}, e); err != nil {
		t.Fatal(err)
	}
	fixture.reg.LoadWidgets([]*metadata.Widget{{Name: "Layout", Type: metadata.WidgetTypeList, Query: "SELECT Наименование FROM Catalog.FilterLayout", Filters: []metadata.WidgetFilter{
		{Name: "Branch", Label: "Филиал", Type: "select", Param: "Branch", Values: []metadata.WidgetFilterValue{{Value: "long", Label: strings.Repeat("Long branch ", 12)}}},
		{Name: "Direction", Label: "Направление", Type: "select", Param: "Direction", Values: []metadata.WidgetFilterValue{{Value: "all", Label: "Все"}}},
		{Name: "Number", Label: "Номер", Type: "string", Param: "Number"},
		{Name: "Status", Label: "Статус", Type: "select", Param: "Status", Values: []metadata.WidgetFilterValue{{Value: "all", Label: "Все"}}},
		{Name: "AllYears", Label: "Все годы", Type: "bool", Param: "AllYears"},
	}}})
	s := New(fixture.reg, fixture.store, fixture.interp, nil, Config{}, nil)
	t.Cleanup(s.Close)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{ID: "layout", Login: "layout", IsAdmin: true})))
		})
	})
	s.MountStatic(router)
	s.Mount(router)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/" {
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
		_, _ = w.Write([]byte(strings.Replace(rec.Body.String(), "</body>", widgetFilterRowMeasureScript+"</body>", 1)))
	}))
	t.Cleanup(func() { s.CloseEventStreams(); server.Close() })
	browser := newMobileLayoutBrowser(t, browserPath)
	measureCtx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	data, err := browser.measure(measureCtx, server.URL+"/ui/")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Count, Rows, NarrowRows int
		Overflow                float64
		ResetAligned            bool
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 5 || got.Rows != 1 || !got.ResetAligned || got.NarrowRows < 2 || got.Overflow > 1 {
		t.Fatalf("five filters + reset row geometry: %s", data)
	}
	t.Logf("mounted default-density list widget: %s", data)
}

const widgetFilterRowMeasureScript = `<script>
window.addEventListener('load',()=>{
 const report=(id,value)=>{const e=document.createElement('pre');e.id=id;e.textContent=value;document.body.appendChild(e)};
 try{
  const card=document.querySelector('.w-card-list'),filters=card.querySelector('.w-filters'),controls=[...filters.querySelectorAll('[data-ob-filter]')],reset=filters.querySelector('[data-ob-filter-reset]');
  if(document.body.classList.contains('ob-compact-lists'))throw Error('fixture must use default density');
  card.style.flex='0 0 800px';card.style.width='800px';card.style.maxWidth='none';
  const rows=()=>new Set(controls.map(e=>Math.round(e.getBoundingClientRect().top))).size;
  const count=controls.length,wideRows=rows(),resetAligned=Math.abs(reset.getBoundingClientRect().top-controls[0].getBoundingClientRect().top)<2;
  card.style.flex='0 0 280px';card.style.width='280px';
  const narrowRows=rows(),right=filters.getBoundingClientRect().right,overflow=Math.max(0,...controls.map(e=>e.getBoundingClientRect().right-right));
  report('list-mobile-measure',JSON.stringify({count,rows:wideRows,narrowRows,overflow,resetAligned}));
 }catch(e){report('list-mobile-error',String(e))}
});</script>`
