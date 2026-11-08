package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Exercise the mounted page, lazy tree transport and production click/keyboard
// handlers together: initial HTML alone cannot detect selection losing contrast.
func TestListRowStyleSelectionBrowser(t *testing.T) {
	browserPath := managedLayoutTestBrowser(t)
	entity := &metadata.Entity{Name: "StyledSelection", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString},
			{Name: "Color", Type: metadata.FieldTypeString},
			{Name: "Bold", Type: metadata.FieldTypeBool},
		},
		Forms: []*metadata.FormModule{{Name: "List", Kind: "list", LayoutKind: metadata.FormLayoutManaged,
			ListRowStyle: &metadata.FormListRowStyle{BackgroundFrom: "Color", BoldFrom: "Bold"},
		}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	s = New(s.reg, s.store, s.interp, nil, Config{}, nil)
	put := func(values map[string]any) string {
		t.Helper()
		id := uuid.New()
		if err := s.store.Upsert(ctx, entity.Name, id, values, entity); err != nil {
			t.Fatal(err)
		}
		return id.String()
	}
	parent := put(map[string]any{"Name": "Folder", "is_folder": true, "Color": "#000000"})
	for _, folder := range []string{"", parent} {
		for _, row := range []struct {
			name, color string
			bold        bool
		}{{"A-dark", "#000000", true}, {"B-light", "#ffffdd", false}, {"C-bold", "", true}, {"D-plain", "", false}} {
			put(map[string]any{"Name": row.name, "Color": row.color, "Bold": row.bold, "parent_id": folder})
		}
	}
	router := chi.NewRouter()
	s.MountStatic(router)
	s.Mount(router)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/catalog/styledselection" {
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
		_, _ = w.Write([]byte(strings.Replace(rec.Body.String(), "</body>", listRowSelectionMeasureScript+"</body>", 1)))
	}))
	t.Cleanup(func() { s.CloseEventStreams(); server.Close(); s.Close() })
	browser := newMobileLayoutBrowser(t, browserPath)
	for _, view := range []string{"list", "tree"} {
		t.Run(view, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			data, err := browser.measure(ctx, server.URL+"/ui/catalog/styledselection?view="+view+"&sort=Name&dir=asc")
			if err != nil {
				t.Fatalf("selection measurement: %v\n%s", err, browser.diagnostics())
			}
			var got struct {
				Initial []struct{ Color, Background, Weight string }
				Samples []struct {
					Phase, Color, Background, Weight string
					Selected, Restored               bool
					Contrast                         float64
				}
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			t.Logf("actual browser styles: %s", data)
			wantSamples := 7
			if view == "tree" {
				wantSamples++
			}
			if len(got.Initial) != 4 || len(got.Samples) != wantSamples {
				t.Fatalf("missing selection controls: %s", data)
			}
			if got.Initial[0].Color != "rgb(255, 255, 255)" || got.Initial[0].Background != "rgb(0, 0, 0)" || got.Initial[0].Weight != "600" {
				t.Fatalf("dark row fixture lost its original style: %+v", got.Initial[0])
			}
			for _, sample := range got.Samples {
				if !sample.Selected || !sample.Restored || sample.Contrast < 4.5 {
					t.Errorf("unreadable selection or lost original style: %+v", sample)
				}
				if (sample.Phase == "click-0" || sample.Phase == "click-2" || sample.Phase == "keyboard-up" || sample.Phase == "keyboard-up-dark") && sample.Weight != "600" {
					t.Errorf("selection lost bold style: %+v", sample)
				}
			}
		})
	}
}

const listRowSelectionMeasureScript = `<script>
window.addEventListener('load', async function () {
 try {
  const result={initial:[],samples:[]};
  function style(row) {
   const cell=getComputedStyle(row.querySelector('td'));
   let bg=cell.backgroundColor;
   if(bg==='rgba(0, 0, 0, 0)') bg=getComputedStyle(row).backgroundColor;
   if(bg==='rgba(0, 0, 0, 0)') bg='rgb(255, 255, 255)';
   return {color:cell.color,background:bg,weight:cell.fontWeight};
  }
  function luminance(rgb) {
   return rgb.match(/[\d.]+/g).slice(0,3).map(Number).map(v=>{
    v/=255;return v<=0.04045?v/12.92:Math.pow((v+0.055)/1.055,2.4);
   }).reduce((sum,v,i)=>sum+v*[0.2126,0.7152,0.0722][i],0);
  }
  function record(phase,row,previous,before) {
   const current=style(row),a=luminance(current.color),b=luminance(current.background);
   result.samples.push({...current,phase,selected:row.getAttribute('aria-selected')==='true',
    restored:!previous || (previous.getAttribute('aria-selected')==='false' && JSON.stringify(style(previous))===JSON.stringify(before)),
    contrast:(Math.max(a,b)+0.05)/(Math.min(a,b)+0.05)});
  }
  const toggle=document.querySelector('.tree-toggle');
  if(new URLSearchParams(location.search).get('view')==='tree') {
   if(!toggle) throw new Error('missing production tree toggle');
   const root=toggle.closest('tr');root.querySelector('td').click();record('tree-root',root);
   toggle.click();
   const deadline=Date.now()+10000;
   while(toggle.dataset.loaded!=='1') {
    if(Date.now()>deadline) throw new Error('lazy children were not loaded');
    await new Promise(resolve=>setTimeout(resolve,20));
   }
  }
  const scope=toggle && new URLSearchParams(location.search).get('view')==='tree'
   ? '[data-tree-parent="'+toggle.dataset.folderId+'"]' : '[data-ob-list-row]';
  const rows=Array.from(document.querySelectorAll(scope)).filter(row=>!row.dataset.isFolder || row.dataset.isFolder!=='1');
  if(rows.length!==4) throw new Error('expected four visible fixture rows, got '+rows.length);
  result.initial=rows.map(style);
  let previous=null,before=null;
  rows.forEach((row,index)=>{
   row.querySelector('td').click();record('click-'+index,row,previous,before);
   previous=row;before=result.initial[index];
  });
  rows[3].dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowUp',bubbles:true,cancelable:true}));
  record('keyboard-up',rows[2],rows[3],result.initial[3]);
  rows[2].dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowUp',bubbles:true,cancelable:true}));
  record('keyboard-up-light',rows[1],rows[2],result.initial[2]);
  rows[1].dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowUp',bubbles:true,cancelable:true}));
  record('keyboard-up-dark',rows[0],rows[1],result.initial[1]);
  const out=document.createElement('script');out.id='list-mobile-measure';out.type='application/json';out.textContent=JSON.stringify(result);document.body.appendChild(out);
 } catch(e) {const out=document.createElement('pre');out.id='list-mobile-error';out.textContent=String(e);document.body.appendChild(out);}
});
</script>`
