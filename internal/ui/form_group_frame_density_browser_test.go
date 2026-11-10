package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/realtime"
)

// Real YAML -> registered HTTP form -> Chromium computed layout. Checking the
// inline style alone misses the density rules overriding it with !important.
func TestManagedGroupFrameFalseDensityBrowser(t *testing.T) {
	browser := newMobileLayoutBrowser(t, managedLayoutTestBrowser(t))
	for _, tc := range []struct {
		density string
		padding [4]float64
		margin  float64
	}{
		{"", [4]float64{12, 14, 12, 14}, 14},
		{"compact", [4]float64{6, 10, 8, 10}, 8},
		{"dense", [4]float64{4, 8, 5, 8}, 5},
	} {
		for _, collapsed := range []bool{true, false} {
			t.Run(fmt.Sprintf("density=%s/collapsed=%t", tc.density, collapsed), func(t *testing.T) {
				source := fmt.Sprintf(`schema: onebase.form/v1
form:
  name: ObjectForm
  kind: object
  entity: Spacing
  density: %q
elements:
  - kind: ГруппаФормы
    name: OuterPlain
    frame: false
    children:
      - kind: ГруппаФормы
        name: InnerPlain
        frame: false
        orientation: horizontal
        width: 600
        height: 80
        children:
          - kind: ПолеВвода
            name: PlainText
            data_path: Объект.Text
          - kind: Кнопка
            name: PlainButton
            title: {ru: Действие}
      - kind: ГруппаФормы
        name: ConditionalPlain
        frame: false
        hidden_when: НЕ ShowExtra
        children:
          - kind: ПолеВвода
            name: ExtraText
            data_path: Объект.Text
  - kind: ГруппаФормы
    name: HeaderSpacer
    frame: false
    height: 6
  - kind: ГруппаФормы
    name: DatesPlain
    frame: false
    orientation: horizontal
    children:
      - kind: ПолеВвода
        name: DateField
        data_path: Объект.Date
  - kind: ГруппаФормы
    name: Framed
    title: {ru: Обычная группа}
    children:
      - kind: ПолеВвода
        name: FramedText
        data_path: Объект.Text
`, tc.density)
				file := filepath.Join(t.TempDir(), "ObjectForm.form.yaml")
				if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
				form, err := loader.NewManagedFormLoader().LoadFormFile(file, "Spacing")
				if err != nil {
					t.Fatal(err)
				}
				entity := &metadata.Entity{Name: "Spacing", Kind: metadata.KindCatalog,
					Fields: []metadata.Field{{Name: "Text", Type: metadata.FieldTypeString}, {Name: "ShowExtra", Type: metadata.FieldTypeBool}, {Name: "Date", Type: metadata.FieldTypeDate}},
					Forms:  []*metadata.FormModule{form}}
				srv, dataCtx := newSubmitTestServer(t, []*metadata.Entity{entity})
				srv.hub = realtime.NewHub()
				id := uuid.New()
				if err := srv.store.Upsert(dataCtx, entity.Name, id, map[string]any{
					"Text": strings.Repeat("Длинные тестовые данные ", 12), "ShowExtra": true,
					"Date": time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
				}, entity); err != nil {
					t.Fatal(err)
				}
				router := chi.NewRouter()
				srv.MountStatic(router)
				srv.Mount(router)
				httpServer := httptest.NewServer(router)
				t.Cleanup(func() { srv.CloseEventStreams(); httpServer.Close() })
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				var target struct{ TargetID string }
				if err := browser.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &target); err != nil {
					t.Fatal(err)
				}
				defer func() {
					closeCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
					defer stop()
					_ = browser.call(closeCtx, "", "Target.closeTarget", map[string]any{"targetId": target.TargetID}, nil)
				}()
				var attached struct{ SessionID string }
				if err := browser.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached); err != nil {
					t.Fatal(err)
				}
				if err := browser.call(ctx, attached.SessionID, "Emulation.setDeviceMetricsOverride", map[string]any{
					"width": 1440, "height": 750, "deviceScaleFactor": 1, "mobile": false,
				}, nil); err != nil {
					t.Fatal(err)
				}
				if err := browser.call(ctx, attached.SessionID, "Page.navigate", map[string]any{"url": httpServer.URL + "/ui/catalog/spacing/" + id.String()}, nil); err != nil {
					t.Fatal(err)
				}
				const expression = `((collapsed) => {
  const el = name => document.querySelector('[data-ob-el="'+name+'"]');
  if (!el('Framed') || document.readyState === 'loading' || !window.__obNavInit) return '';
  if (document.documentElement.classList.contains('nav-collapsed') !== collapsed) {
    document.querySelector('[data-ob-nav-toggle]').click();
    return '';
  }
  const box = name => {
    const e = el(name), s = getComputedStyle(e), r = e.getBoundingClientRect();
    return {padding: [s.paddingTop,s.paddingRight,s.paddingBottom,s.paddingLeft].map(parseFloat),
      margin: [s.marginTop,s.marginRight,s.marginBottom,s.marginLeft].map(parseFloat),
      border: parseFloat(s.borderLeftWidth), left:r.left, top:r.top, bottom:r.bottom, width:r.width, height:r.height};
  };
  const input = el('PlainText').querySelector('input').getBoundingClientRect();
  const button = el('PlainButton').getBoundingClientRect();
  return JSON.stringify({outer:box('OuterPlain'), inner:box('InnerPlain'), framed:box('Framed'),
    extra:box('ConditionalPlain'), hasLongValue:el('PlainText').querySelector('input').value.length > 200,
    spacer:box('HeaderSpacer'), dates:box('DatesPlain'),
    navCollapsed:document.documentElement.classList.contains('nav-collapsed'),
    inputLeft:input.left, inputTop:input.top, buttonTop:button.top, viewport:[innerWidth,innerHeight]});
})(%t)`
				var raw string
				for raw == "" {
					var evaluation struct {
						Result           struct{ Value string }
						ExceptionDetails json.RawMessage
					}
					if err := browser.call(ctx, attached.SessionID, "Runtime.evaluate", map[string]any{
						"expression": fmt.Sprintf(expression, collapsed), "returnByValue": true,
					}, &evaluation); err != nil {
						t.Fatalf("browser measurement: %v\n%s", err, browser.diagnostics())
					}
					if len(evaluation.ExceptionDetails) != 0 {
						t.Fatalf("measurement script: %s", evaluation.ExceptionDetails)
					}
					raw = evaluation.Result.Value
					if raw == "" {
						select {
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						case <-time.After(20 * time.Millisecond):
						}
					}
				}
				type box struct {
					Padding, Margin     [4]float64
					Border, Left, Width float64
					Height, Top, Bottom float64
				}
				var got struct {
					Outer, Inner, Framed, Extra, Spacer, Dates box
					HasLongValue, NavCollapsed                 bool
					InputLeft, InputTop, ButtonTop             float64
					Viewport                                   [2]int
				}
				if err := json.Unmarshal([]byte(raw), &got); err != nil {
					t.Fatal(err)
				}
				t.Log(raw)
				if got.Viewport != [2]int{1440, 750} {
					t.Errorf("viewport = %v", got.Viewport)
				}
				if !got.HasLongValue || got.Extra.Height <= 0 || got.NavCollapsed != collapsed {
					t.Errorf("long data, visible conditional block or navigation state not exercised: %s", raw)
				}
				for name, plain := range map[string]box{"outer": got.Outer, "inner": got.Inner, "conditional": got.Extra, "spacer": got.Spacer, "dates": got.Dates} {
					if plain.Padding != [4]float64{} || plain.Margin != [4]float64{} || plain.Border != 0 {
						t.Errorf("%s frame:false acquired spacing: %+v", name, plain)
					}
				}
				if got.Framed.Padding != tc.padding || got.Framed.Margin[2] != tc.margin || got.Framed.Border != 1 {
					t.Errorf("framed spacing changed: %+v", got.Framed)
				}
				if got.Inner.Width != 600 || got.Inner.Height != 80 {
					t.Errorf("frame:false lost explicit width/height: %+v", got.Inner)
				}
				if got.Spacer.Height != 6 || math.Abs(got.Dates.Top-got.Outer.Bottom-6) > 0.5 {
					t.Errorf("intentional header/date gap must remain exactly 6px: %s", raw)
				}
				if math.Abs(got.InputLeft-got.Outer.Left) > 1 || (tc.density != "" && math.Abs(got.InputTop-got.ButtonTop) > 1) {
					t.Errorf("nested plain row is shifted or button misaligned: %s", raw)
				}
			})
		}
	}
}
