package launcher

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// Start with the real GET response, submit its controls, then reload both YAML
// and the editor. A hand-built payload alone cannot detect a missing checkbox.
func TestConfiguratorMultilineHTTP(t *testing.T) {
	for _, section := range []string{"field", "dim", "res"} {
		for _, change := range []string{"preserve", "disable", "retype", "legacy-retype", "enable", "new-field"} {
			t.Run(section+"/"+change, func(t *testing.T) {
				h, dir := newFileBaseHandler(t)
				h.runner = NewRunner()
				sub, key, action := "catalogs", "fields", "/configurator/fields"
				save := h.configuratorSaveFields
				if section != "field" {
					sub, action, save = "inforegs", "/configurator/inforeg-fields", h.configuratorSaveInfoRegFields
					key = "dimensions"
					if section == "res" {
						key = "resources"
					}
				}
				initial := "true"
				if change == "enable" {
					initial = "false"
				}
				path := writeCfgFile(t, dir, sub, "заметки.yaml", "name: Заметки\n"+key+":\n  - name: Текст\n    type: string\n    multiline: "+initial+"\n")
				router := chi.NewRouter()
				router.Get("/bases/{id}/configurator", h.configuratorPage)
				get := func() url.Values {
					t.Helper()
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bases/test/configurator", nil))
					if rec.Code != http.StatusOK {
						t.Fatalf("GET status=%d: %s", rec.Code, rec.Body.String())
					}
					return browserSubmit(t, rec.Body.String(), action)
				}
				form := get()
				prefix := section + ".0"
				if form.Get(prefix+".multiline_present") != "1" {
					t.Fatal("GET did not expose the multiline editor")
				}
				if got := form.Get(prefix + ".multiline"); (got == "1") != (initial == "true") {
					t.Fatalf("checkbox=%q, YAML multiline=%s", got, initial)
				}
				wantType, wantMultiline := "string", true
				switch change {
				case "disable":
					form.Del(prefix + ".multiline")
					wantMultiline = false
				case "retype", "legacy-retype":
					form.Set(prefix+".type", "number")
					wantType, wantMultiline = "number", false
					if change == "legacy-retype" {
						form.Del(prefix + ".multiline_present")
						form.Del(prefix + ".multiline")
					}
				case "enable":
					form.Set(prefix+".multiline", "1")
				case "new-field":
					prefix = "new_" + section + ".9"
					form.Set(prefix+".name", "Новое")
					form.Set(prefix+".type", "string")
					form.Set(prefix+".multiline_present", "1")
					form.Set(prefix+".multiline", "1")
				}
				rec := postCfg(t, "test", "/bases/test"+action, form, save)
				if ok, message := cfgResponse(t, rec); !ok {
					t.Fatalf("POST failed: %s", message)
				}
				var saved map[string]any
				if err := yaml.Unmarshal([]byte(readCfg(t, path)), &saved); err != nil {
					t.Fatal(err)
				}
				index := 0
				if change == "new-field" {
					index = 1
				}
				field := saved[key].([]any)[index].(map[string]any)
				if field["type"] != wantType || (field["multiline"] == true) != wantMultiline {
					t.Fatalf("saved field=%v, want type=%s multiline=%v", field, wantType, wantMultiline)
				}
				if wantType != "string" && field["multiline"] != nil {
					t.Fatalf("non-string field retained multiline: %v", field)
				}
				prefix = section + ".0"
				if index == 1 {
					prefix = section + ".1"
				}
				if got := get().Get(prefix + ".multiline"); (got == "1") != wantMultiline {
					t.Fatalf("reloaded checkbox=%q, want checked=%v", got, wantMultiline)
				}
			})
		}
	}
}
