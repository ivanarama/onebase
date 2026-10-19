package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Use mounted production pages and assets: checking the overflow wrapper alone
// misses a flex item whose intrinsic width expands beyond the mobile viewport.
func TestListTablesMobileBrowserGeometry(t *testing.T) {
	browserPath := managedLayoutTestBrowser(t)
	document := &metadata.Entity{Name: "Orders", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Title", Type: metadata.FieldTypeString}}}
	info := &metadata.InfoRegister{Name: "Rates",
		Dimensions: []metadata.Field{{Name: "Warehouse", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Rate", Type: metadata.FieldTypeNumber}}}
	journal := &metadata.Journal{Name: "OrderJournal", Documents: []string{document.Name},
		Columns: []metadata.JournalColumn{{Field: "Title", Label: "Title", Map: map[string]string{document.Name: "Title"}}},
		Filters: []metadata.JournalFilter{{Field: "Title", Type: "string"}}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{document})
	if err := s.store.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{info}); err != nil {
		t.Fatal(err)
	}
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{document}, InfoRegs: []*metadata.InfoRegister{info}})
	s.reg.LoadJournals([]*metadata.Journal{journal})
	value := strings.Repeat("ДлинноеНеразрывноеЗначение", 30)
	if err := s.store.Upsert(ctx, document.Name, uuid.New(), map[string]any{"Title": value}, document); err != nil {
		t.Fatal(err)
	}
	if err := s.store.InfoRegSet(ctx, info, map[string]any{"Warehouse": value}, map[string]any{"Rate": 1.5}, nil); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	s.MountStatic(router)
	s.Mount(router)
	// Only append measurement code. HTML, data, styles and interaction handlers
	// all come from Server.Mount, and asset requests go through MountStatic.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/test-viewport" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			// Chrome's CLI clamps a top-level window to 500px on some systems.
			// An iframe gives the production document its exact CSS viewport.
			_, _ = fmt.Fprintf(w, `<html><body><iframe style="border:0" width="%s" height="900" src="%s"></iframe></body></html>`, html.EscapeString(r.URL.Query().Get("width")), html.EscapeString(r.URL.Query().Get("page")))
			return
		}
		if r.URL.Path != "/ui/inforeg/rates" && r.URL.Path != "/ui/journal/orderjournal" {
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
		page := rec.Body.String()
		page = strings.Replace(page, "</body>", listMobileMeasureScript+"</body>", 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(func() { s.CloseEventStreams(); server.Close() })
	browser := newMobileLayoutBrowser(t, browserPath)
	for _, width := range []int{390, 1280} {
		for _, target := range []string{"/ui/inforeg/rates", "/ui/journal/orderjournal"} {
			for _, empty := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d%s/empty=%t", width, target, empty), func(t *testing.T) {
					path := target
					if empty {
						if strings.Contains(path, "inforeg") {
							path += "?flt_Warehouse=no-match"
						} else {
							path += "?f.Title=no-match"
						}
					}
					got := measureListMobilePage(t, browser, server.URL+"/test-viewport?"+url.Values{"width": {fmt.Sprint(width)}, "page": {path}}.Encode())
					if got.Viewport != width {
						t.Fatalf("viewport=%d, want %d", got.Viewport, width)
					}
					if got.Height != 900 {
						t.Fatalf("viewport height=%d, want 900", got.Height)
					}
					if !empty && !got.HasLongValue {
						t.Fatal("long fixture value missing from visible row")
					}
					if got.HasRow == empty {
						t.Fatalf("row presence=%t, empty=%t", got.HasRow, empty)
					}
					if got.HasEmpty != empty {
						t.Fatalf("empty state=%t, want %t", got.HasEmpty, empty)
					}
					for _, phase := range got.Phases {
						t.Logf("%s: %+v", phase.Name, phase)
						if phase.CardWidth <= 0 || phase.CardLeft < phase.WrapLeft-1 || phase.CardRight > phase.WrapRight+1 {
							t.Errorf("%s: card outside list wrapper: %+v", phase.Name, phase)
						}
						if phase.PageWidth > got.Viewport+1 {
							t.Errorf("%s: page overflows viewport: %d > %d", phase.Name, phase.PageWidth, got.Viewport)
						}
						if !empty && (phase.ScrollWidth <= phase.ClientWidth || phase.ScrollLeft <= 0) {
							t.Errorf("%s: wide table does not scroll inside card: %+v", phase.Name, phase)
						}
						if phase.Name == "details" {
							if phase.PanelWidth <= 0 || (!empty && !phase.DetailHasValue) {
								t.Errorf("detail panel missing content: %+v", phase)
							}
							if width == 390 && phase.PanelTop < phase.CardBottom {
								t.Errorf("mobile details are not below list: %+v", phase)
							}
							if width == 1280 && phase.PanelLeft < phase.CardRight {
								t.Errorf("desktop details are not beside list: %+v", phase)
							}
						} else if phase.PanelWidth != 0 {
							t.Errorf("%s: closed panel still visible", phase.Name)
						}
					}
					if len(got.Phases) != 3 {
						t.Fatalf("expected closed/open/closed measurements: %+v", got)
					}
				})
			}
		}
	}
}

const listMobileMeasureScript = `<script>
window.addEventListener('load', function () {
 try {
  const wrap=document.querySelector('.ob-list-wrap');
  const card=wrap.querySelector(':scope > .card');
  const row=card.querySelector('[data-ob-list-row]');
  const toggle=document.querySelector('[data-ob-detail-toggle]');
  const panel=document.getElementById('ob-detail');
  const scroll=card.querySelector('div[style="overflow-x:auto"]');
  const result={viewport:window.innerWidth,height:window.innerHeight,hasLongValue:!!row && row.textContent.includes('ДлинноеНеразрывноеЗначение'),hasRow:!!row,hasEmpty:!!card.querySelector('.empty'),phases:[]};
  function measure(name) {
   const c=card.getBoundingClientRect(),w=wrap.getBoundingClientRect(),p=panel.getBoundingClientRect();
   // Mobile CSS scrolls the table itself; desktop scrolls its wrapper.
   const scroller=[scroll,scroll && scroll.querySelector('table')].find(el => el && el.scrollWidth>el.clientWidth) || scroll;
   if(scroller) scroller.scrollLeft=80;
   result.phases.push({name,cardWidth:c.width,cardLeft:c.left,cardRight:c.right,cardBottom:c.bottom,
    wrapLeft:w.left,wrapRight:w.right,pageWidth:document.documentElement.scrollWidth,
    scrollWidth:scroller?scroller.scrollWidth:0,clientWidth:scroller?scroller.clientWidth:0,scrollLeft:scroller?scroller.scrollLeft:0,
    panelWidth:p.width,panelLeft:p.left,panelTop:p.top,detailHasValue:panel.textContent.includes('ДлинноеНеразрывноеЗначение')});
  }
  if(!toggle || !panel) throw new Error('Production detail controls missing');
  if(toggle.getAttribute('aria-expanded')==='true') toggle.click();
  measure('closed');
  if(row) row.click();
  toggle.click();
  measure('details');
  toggle.click();
  measure('closed-again');
  const out=document.createElement('script');out.id='list-mobile-measure';out.type='application/json';out.textContent=JSON.stringify(result);window.parent.document.body.appendChild(out);
 } catch(e) {const out=document.createElement('pre');out.id='list-mobile-error';out.textContent=String(e);window.parent.document.body.appendChild(out);}
});
</script>`

type listMobilePhase struct {
	Name                                                            string
	CardWidth, CardLeft, CardRight, CardBottom, WrapLeft, WrapRight float64
	PanelWidth, PanelLeft, PanelTop                                 float64
	PageWidth, ScrollWidth, ClientWidth, ScrollLeft                 int
	DetailHasValue                                                  bool
}
type listMobileMeasurement struct {
	Viewport, Height               int
	HasRow, HasEmpty, HasLongValue bool
	Phases                         []listMobilePhase
}

func measureListMobilePage(t *testing.T, browser *mobileLayoutBrowser, target string) listMobileMeasurement {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	data, err := browser.measure(ctx, target)
	if err != nil {
		t.Fatalf("browser measurement: %v\n%s", err, browser.diagnostics())
	}
	var got listMobileMeasurement
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("measurement: %v", err)
	}
	return got
}
