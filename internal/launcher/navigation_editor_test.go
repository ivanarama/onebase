package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"gopkg.in/yaml.v3"
)

const navigationEditorFixture = `# school configuration
name: School
title: School
titles:
  en: School in English
# unrelated permissions
roles: [Teacher]
future_flag: preserve
contents:
  catalogs: [B, A]
  documents: [Order]
home_page:
  title: Dashboard
  future_home: keep
menu:
  # section comment
  sections:
    - id: education
      title: Education
      titles:
        en: English education
      items:
        - id: a
          target: catalog:A
        - id: order
          target: document:Order
      groups:
        - id: year
          title: Year
          items:
            # item B travels with this comment
            - id: b
              target: catalog:B
`

func newNavigationEditorFixture(t *testing.T, mode, subPath string) (*handler, *Base) {
	t.Helper()
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	previous := launcherBundle
	launcherBundle = bundle
	t.Cleanup(func() { launcherBundle = previous })
	store := newTestStore(t)
	b := &Base{ID: "test", Name: "Test", ConfigSource: mode, Path: t.TempDir(), DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "base.db")}
	if err := store.Add(b); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}
	files := []configdb.ConfigFile{
		{Path: "config/app.yaml", Content: []byte("name: Test\n")},
		{Path: "catalogs/a.yaml", Content: []byte("name: A\nfields: []\n")},
		{Path: "catalogs/b.yaml", Content: []byte("name: B\nfields: []\n")},
		{Path: "documents/order.yaml", Content: []byte("name: Order\nfields: []\n")},
		{Path: subPath, Content: []byte(navigationEditorFixture)},
		{Path: "tree_order.yaml", Content: []byte("catalogs: [A, B]\n")},
		{Path: "config/home_page.yaml", Content: []byte("# dashboard comment\ntitle: Home\ntitles:\n  en: English home\nfuture_home: preserve\nnav:\n  catalogs: [A]\nmenu:\n  sections:\n    - id: home\n      title: Home\n      items:\n        - id: home-a\n          target: catalog:A\n")},
	}
	if mode == "database" {
		db, err := OpenDB(context.Background(), b)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		repo := configdb.New(db)
		if err := repo.EnsureSchema(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := repo.SaveFiles(context.Background(), files, configdb.VersionOptions{Message: "fixture"}); err != nil {
			t.Fatal(err)
		}
	} else {
		for _, file := range files {
			full := filepath.Join(b.Path, filepath.FromSlash(file.Path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, file.Content, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return h, b
}

// These requests use ListenAndServe's actual route table, authentication and
// write guards. A test-only copy of a route cannot prove access protection.
func TestNavigationEditorProductionRoutesAndOldForms(t *testing.T) {
	for _, mode := range []string{"file", "database"} {
		t.Run(mode, func(t *testing.T) {
			h, b := newNavigationEditorFixture(t, mode, "subsystems/school.yaml")
			db, err := OpenDB(context.Background(), b)
			if err != nil {
				t.Fatal(err)
			}
			repo := auth.NewRepo(db)
			if err := repo.EnsureSchema(context.Background()); err != nil {
				t.Fatal(err)
			}
			tokens := map[string]string{}
			for _, role := range []string{"admin", "ordinary"} {
				user, err := repo.Create(context.Background(), role, "Str0ng-Passw0rd!", role, role == "admin")
				if err != nil {
					t.Fatal(err)
				}
				token, err := repo.CreateSession(context.Background(), user.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
				if err != nil {
					t.Fatal(err)
				}
				tokens[role] = token
			}
			db.Close()
			srv, err := NewServer(h.store, h.runner)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- srv.ListenAndServe() }()
			t.Cleanup(func() { srv.Close(); <-done })
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			call := func(method, path, body, contentType, role string) (int, string) {
				t.Helper()
				r, err := http.NewRequest(method, srv.URL()+"/bases/test/configurator"+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Content-Type", contentType)
				r.Header.Set("Accept-Language", "en")
				if token := tokens[role]; token != "" {
					r.AddCookie(&http.Cookie{Name: configuratorSessionCookieName, Value: token})
				}
				res, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = res.Body.Close() }()
				raw, err := io.ReadAll(res.Body)
				if err != nil {
					t.Fatal(err)
				}
				return res.StatusCode, string(raw)
			}
			data := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "?subsystem=School", ""))
			body := navigationEditorBody(t, "School", data.Menu)
			for _, role := range []string{"", "ordinary"} {
				for _, path := range []string{"/navigation?subsystem=School", "/navigation/preview", "/navigation/save"} {
					method := http.MethodPost
					if strings.Contains(path, "?") {
						method = http.MethodGet
					}
					code, _ := call(method, path, body, "application/json", role)
					if code != http.StatusFound {
						t.Fatalf("%q %s: auth status %d", role, path, code)
					}
				}
			}
			raw, _ := h.readConfigFileRaw(context.Background(), b, "subsystems/school.yaml")
			if string(raw) != navigationEditorFixture {
				t.Fatal("unauthorized request changed YAML")
			}
			code, html := call(http.MethodGet, "/navigation?subsystem=School", "", "", "admin")
			if code != 200 || !strings.Contains(html, "Menu editor") || !strings.Contains(html, "/static/navigation-editor.js") {
				t.Fatalf("editor page %d: %s", code, html)
			}
			for _, path := range []string{"/navigation/preview", "/navigation/save"} {
				if code, text := call(http.MethodPost, path, body, "application/json", "admin"); code != 200 {
					t.Fatalf("admin %s: %d %s", path, code, text)
				}
			}
			before, _ := h.readConfigFileRaw(context.Background(), b, "subsystems/school.yaml")
			if code, _ := call(http.MethodPost, "/navigation/save", body, "application/x-www-form-urlencoded", "admin"); code != 400 {
				t.Fatalf("JSON accepted as form: %d", code)
			}
			after, _ := h.readConfigFileRaw(context.Background(), b, "subsystems/school.yaml")
			if !bytes.Equal(before, after) {
				t.Fatal("wrong content type changed YAML")
			}
			form := url.Values{"subsystem_name": {"School"}, "title": {"Updated school"}, "catalogs": {"B", "A"}, "documents": {"Order"}}.Encode()
			if code, text := call(http.MethodPost, "/subsystem", form, "application/x-www-form-urlencoded", "admin"); code != 200 {
				t.Fatalf("old subsystem form: %d %s", code, text)
			}
			raw, _ = h.readConfigFileRaw(context.Background(), b, "subsystems/school.yaml")
			for _, text := range []string{"id: education", "en: English education", "# item B travels with this comment", "future_flag: preserve"} {
				if !strings.Contains(string(raw), text) {
					t.Fatalf("old subsystem form lost %q", text)
				}
			}
			global := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "", ""))
			global.Menu.Sections[0].Title, global.Menu.Sections[0].Icon = "Dashboard menu", "home"
			if code, text := call(http.MethodPost, "/navigation/save", navigationEditorBody(t, "", global.Menu), "application/json", "admin"); code != 200 {
				t.Fatalf("global save: %d %s", code, text)
			}
			if code, text := call(http.MethodPost, "/home-page", "home_title=New+dashboard", "application/x-www-form-urlencoded", "admin"); code != 200 {
				t.Fatalf("old home form: %d %s", code, text)
			}
			raw, _ = h.readConfigFileRaw(context.Background(), b, "config/home_page.yaml")
			for _, text := range []string{"# dashboard comment", "future_home: preserve", "en: English home", "catalogs: [A]", "title: Dashboard menu", "icon: home", "id: home-a"} {
				if !strings.Contains(string(raw), text) {
					t.Fatalf("old home form lost %q:\n%s", text, raw)
				}
			}
			{
				node, err := exec.LookPath("node")
				if err != nil {
					t.Skip("node is not installed")
				}
				path := filepath.Join(t.TempDir(), "navigation-editor.html")
				if err := os.WriteFile(path, []byte(html), 0o600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(node, "--test", "navigation_editor_behavior_test.js") //nolint:gosec // Test-only executable resolved with LookPath.
				// Capture an actual page for a valid empty menu in both storage modes.
				emptyYAML := strings.Split(navigationEditorFixture, "\nmenu:")[0] + "\nmenu: {}\n"
				if err := h.saveConfigFile(context.Background(), b, "subsystems/school.yaml", []byte(emptyYAML)); err != nil {
					t.Fatal(err)
				}
				code, emptyHTML := call(http.MethodGet, "/navigation?subsystem=School", "", "", "admin")
				if code != http.StatusOK {
					t.Fatalf("empty editor page: %d %s", code, emptyHTML)
				}
				emptyPath := filepath.Join(t.TempDir(), "empty-navigation-editor.html")
				if err := os.WriteFile(emptyPath, []byte(emptyHTML), 0o600); err != nil {
					t.Fatal(err)
				}
				cmd.Env = append(os.Environ(), "ONEBASE_NAVIGATION_EDITOR_HTML="+path, "ONEBASE_NAVIGATION_EDITOR_EMPTY_HTML="+emptyPath)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("production editor behavior: %v\n%s", err, output)
				}
			}
		})
	}
}

// Exercise HTTP routing and JSON boundaries, not a private YAML transformer.
func navigationEditorHTTP(h *handler, method, endpoint, body string, lang ...string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Get("/bases/{id}/configurator/navigation", h.configuratorNavigation)
	router.Post("/bases/{id}/configurator/navigation/save", h.configuratorNavigationSave)
	router.Post("/bases/{id}/configurator/navigation/preview", h.configuratorNavigationPreview)
	r := httptest.NewRequest(method, "/bases/test/configurator/navigation"+endpoint, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	if len(lang) > 0 {
		r.Header.Set("Accept-Language", lang[0])
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	return rec
}

func readNavigationEditorData(t *testing.T, rec *httptest.ResponseRecorder) navigationEditorData {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var data navigationEditorData
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func navigationEditorBody(t *testing.T, sub string, menu *metadata.Menu) string {
	t.Helper()
	raw, err := json.Marshal(navigationEditorRequest{Subsystem: sub, Menu: menu, Lang: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestNavigationEditorHTTPStorageRoundTrip(t *testing.T) {
	var fileResult []byte
	for _, mode := range []string{"file", "database"} {
		t.Run(mode, func(t *testing.T) {
			const subPath = "subsystems/SchoolCustom.yaml"
			h, b := newNavigationEditorFixture(t, mode, subPath)
			data := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "?subsystem=School", ""))
			if len(data.Palette) != 3 || data.Menu.Sections[0].ID != "education" {
				t.Fatalf("incorrect editor context: %+v", data)
			}
			s := &data.Menu.Sections[0]
			s.Title, s.Icon = "Lessons", "book-open"
			bItem := s.Groups[0].Items[0]
			s.Groups = []metadata.MenuGroup{{ID: "mixed", Title: "School year", Icon: "folder", Items: []metadata.MenuItem{bItem, s.Items[1]}}}
			s.Items = nil // A is unplaced and must return to Other.
			body := navigationEditorBody(t, "School", data.Menu)
			preview := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodPost, "/preview", body))
			if len(preview.Preview) != 2 || preview.Preview[0].Title != "Lessons" || len(preview.Preview[0].Groups[0].Items) != 2 || preview.Preview[1].ID != "cfg:other" || preview.Preview[1].Items[0].Label != "A" {
				t.Fatalf("runtime preview lost mixed group or Other: %+v", preview.Preview)
			}
			before, _ := h.readConfigFileRaw(context.Background(), b, subPath)
			if string(before) != navigationEditorFixture {
				t.Fatal("preview wrote configuration")
			}
			readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodPost, "/save", body))
			got, ok := h.readConfigFileRaw(context.Background(), b, subPath)
			if !ok {
				t.Fatal("source file disappeared")
			}
			for _, text := range []string{"# school configuration", "# unrelated permissions", "roles: [Teacher]", "future_flag: preserve", "future_home: keep", "catalogs: [B, A]", "en: English education", "# section comment", "# item B travels with this comment", "id: mixed", "title: Lessons", "icon: book-open"} {
				if !strings.Contains(string(got), text) {
					t.Errorf("lost %q:\n%s", text, got)
				}
			}
			if _, exists := h.readConfigFileRaw(context.Background(), b, "subsystems/school.yaml"); exists {
				t.Fatal("save created a second file derived from the object name")
			}
			var saved struct {
				Menu *metadata.Menu `yaml:"menu"`
			}
			if err := yaml.Unmarshal(got, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Menu.Sections[0].Groups[0].Items[0].ID != "b" || saved.Menu.Sections[0].Titles["en"] != "English education" {
				t.Fatal("move changed identity or translations")
			}
			if mode == "file" {
				fileResult = got
			} else if !bytes.Equal(fileResult, got) {
				t.Fatalf("file/database YAML differs:\nfile:\n%s\ndatabase:\n%s", fileResult, got)
			}
		})
	}
}

func TestNavigationEditorImportIsReadOnlyAndPreservesContentsOrder(t *testing.T) {
	for _, mode := range []string{"file", "database"} {
		t.Run(mode, func(t *testing.T) {
			h, b := newNavigationEditorFixture(t, mode, "subsystems/school.yaml")
			for _, kind := range []string{"legacy", "tree-order"} {
				data := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "?subsystem=School&import="+kind, ""))
				if data.Error != "" || data.Menu.Sections[0].Items[0].Target != "catalog:B" || data.Menu.Sections[0].Items[1].Target != "catalog:A" {
					t.Fatalf("import ignored explicit contents order: %+v", data)
				}
				raw, _ := h.readConfigFileRaw(context.Background(), b, "subsystems/school.yaml")
				if string(raw) != navigationEditorFixture {
					t.Fatal("import wrote menu before Save")
				}
			}
			global := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "", ""))
			if len(global.Palette) != 1 || global.Palette[0].Target != "catalog:A" {
				t.Fatalf("global scoped nav expanded membership: %+v", global.Palette)
			}
		})
	}
}

func TestNavigationEditorBadBodiesDoNotWrite(t *testing.T) {
	for _, mode := range []string{"file", "database"} {
		t.Run(mode, func(t *testing.T) {
			h, b := newNavigationEditorFixture(t, mode, "subsystems/school.yaml")
			cases := []string{
				"{", "null", "{}", "subsystem=School&menu=bad", "{} {}",
				`{"subsystem":"School","menu":{"sections":[]},"roles":["Admin"]}`,
				`{"subsystem":"School","menu":{"sections":[{"id":"x","title":"X","items":[{"id":"bad","target":"catalog:Outside"}]}]}}`,
				`{"subsystem":"School","menu":{"sections":[{"id":"x","title":"X","groups":[{"id":"y","title":"Y","groups":[]}]}]}}`,
				strings.Repeat(" ", maxNavigationEditorBody+1),
				string([]byte{'{', '"', 0xff, '"', ':', '1', '}'}),
			}
			tooMany := &metadata.Menu{}
			for i := 0; i <= 100; i++ {
				tooMany.Sections = append(tooMany.Sections, metadata.MenuSection{ID: fmt.Sprintf("s-%d", i), Title: "Section"})
			}
			cases = append(cases, navigationEditorBody(t, "School", tooMany))
			for i, body := range cases {
				rec := navigationEditorHTTP(h, http.MethodPost, "/save", body)
				if rec.Code < 400 {
					t.Errorf("case %d accepted malformed/oversize input: %s", i, rec.Body.String())
				}
				raw, _ := h.readConfigFileRaw(context.Background(), b, "subsystems/school.yaml")
				if string(raw) != navigationEditorFixture {
					t.Fatalf("case %d changed YAML", i)
				}
			}
		})
	}
}

func TestNavigationEditorFlatImportAndAliases(t *testing.T) {
	for _, mode := range []string{"file", "database"} {
		t.Run(mode, func(t *testing.T) {
			h, b := newNavigationEditorFixture(t, mode, "subsystems/school.yaml")
			ctx := context.Background()
			global := []byte("# global dashboard\ntitle: Home\n")
			if err := h.writeConfigFileRaw(ctx, b, "config/home_page.yaml", global); err != nil {
				t.Fatal(err)
			}
			if err := h.writeConfigFileRaw(ctx, b, "registers/attendance.yaml", []byte("name: Attendance\ndimensions: []\nresources: []\n")); err != nil {
				t.Fatal(err)
			}
			if err := h.saveTreeOrderGroupFor(ctx, b, "catalogs", []string{"B", "A"}); err != nil {
				t.Fatal(err)
			}
			if err := h.saveTreeOrderGroupFor(ctx, b, "groups", []string{"documents", "catalogs"}); err != nil {
				t.Fatal(err)
			}
			legacy := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "?import=legacy", ""))
			tree := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "?import=tree-order", ""))
			if tree.Menu.Sections[0].Items[0].Target != "document:Order" || tree.Menu.Sections[1].Items[0].Target != "catalog:B" {
				t.Fatalf("tree hint ignored: %+v", tree.Menu)
			}
			if legacy.Menu.Sections[0].Items[0].Target != "catalog:A" {
				t.Fatalf("legacy flat order changed: %+v", legacy.Menu)
			}
			targets := map[string]bool{}
			for _, section := range tree.Menu.Sections {
				for _, item := range section.Items {
					targets[item.Target] = true
				}
			}
			if !targets["register:Attendance:movements"] || !targets["register:Attendance:balances"] {
				t.Fatalf("register views lost: %+v", targets)
			}
			raw, _ := h.readConfigFileRaw(ctx, b, "config/home_page.yaml")
			if !bytes.Equal(raw, global) {
				t.Fatal("flat import wrote YAML")
			}
			readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodPost, "/save", navigationEditorBody(t, "", tree.Menu)))
			if err := h.writeConfigFileRaw(ctx, b, "catalogs/a.yaml", []byte("name: A\ntitle: First\ntitles:\n  en: Zulu\nfields: []\n")); err != nil {
				t.Fatal(err)
			}
			if err := h.writeConfigFileRaw(ctx, b, "catalogs/b.yaml", []byte("name: B\ntitle: Last\ntitles:\n  en: Alpha\nfields: []\n")); err != nil {
				t.Fatal(err)
			}
			english := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "?import=legacy", "", "en"))
			if english.Menu.Sections[0].Items[0].Target != "catalog:B" || english.Preview[0].Items[0].Label != "Alpha" {
				t.Fatalf("import did not follow translated runtime order: %+v", english)
			}
			for _, original := range []string{
				"title: Home\nmenu: &shared\n  sections: []\nexternal: *shared\n",
				"title: Home\ndefaults: &shared\n  menu:\n    sections: []\n<<: *shared\n",
			} {
				if err := h.writeConfigFileRaw(ctx, b, "config/home_page.yaml", []byte(original)); err != nil {
					t.Fatal(err)
				}
				rec := navigationEditorHTTP(h, http.MethodPost, "/save", navigationEditorBody(t, "", tree.Menu))
				if rec.Code != 400 {
					t.Fatalf("shared YAML menu accepted: %d %s", rec.Code, rec.Body.String())
				}
				raw, _ := h.readConfigFileRaw(ctx, b, "config/home_page.yaml")
				if string(raw) != original {
					t.Fatal("failed alias save changed graph")
				}
			}
		})
	}
}

// A blocked staging file must fail the public save without touching the source.
// This also catches an accidental return to direct writes of the YAML file.
func TestNavigationEditorFailedSaveKeepsSourceYAML(t *testing.T) {
	for _, target := range []struct{ path, subsystem string }{
		{"subsystems/school.yaml", "School"},
		{"config/home_page.yaml", ""},
	} {
		t.Run(target.path, func(t *testing.T) {
			h, b := newNavigationEditorFixture(t, "file", "subsystems/school.yaml")
			full := filepath.Join(b.Path, filepath.FromSlash(target.path))
			before, err := os.ReadFile(full)
			if err != nil {
				t.Fatal(err)
			}
			data := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "?subsystem="+target.subsystem, ""))
			data.Menu.Sections[0].Title = "Changed title"
			if err := os.Mkdir(full+".tmp", 0o700); err != nil {
				t.Fatal(err)
			}
			res := navigationEditorHTTP(h, http.MethodPost, "/save", navigationEditorBody(t, target.subsystem, data.Menu))
			if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"error"`) {
				t.Fatalf("failed save: HTTP %d %s", res.Code, res.Body.String())
			}
			after, err := os.ReadFile(full)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("failed save changed source YAML")
			}
		})
	}
}
