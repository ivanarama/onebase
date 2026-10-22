package launcher

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/version"
)

func TestCfgAboutDistributionHTTP(t *testing.T) {
	name, ver, supplier := version.DistributionName, version.DistributionVersion, version.DistributionSupplier
	t.Cleanup(func() {
		version.DistributionName, version.DistributionVersion, version.DistributionSupplier = name, ver, supplier
	})
	savedBundle := launcherBundle
	t.Cleanup(func() { launcherBundle = savedBundle })
	store := baseOnSQLite(t, "distribution", filepath.Join(t.TempDir(), "base.db"))
	srv, err := NewServer(store, NewRunner())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	client := &http.Client{Timeout: 10 * time.Second}
	t.Cleanup(func() {
		client.CloseIdleConnections()
		srv.Close()
		if err := <-done; err != nil && err != http.ErrServerClosed {
			t.Error(err)
		}
	})
	for _, fork := range []bool{false, true} {
		version.DistributionName, version.DistributionVersion, version.DistributionSupplier = "", "", ""
		if fork {
			version.DistributionName, version.DistributionVersion, version.DistributionSupplier = "<Fork>", "2.1", "<Supplier>"
		}
		req, err := http.NewRequestWithContext(t.Context(), "GET", srv.URL()+"/bases/distribution/configurator/admin/about", nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(res.Body)
		closeErr := res.Body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read=%v close=%v", readErr, closeErr)
		}
		body := string(data)
		if res.StatusCode != 200 {
			t.Fatalf("about: %d %s", res.StatusCode, body)
		}
		if strings.Contains(body, "Изменённая сборка") != fork {
			t.Fatalf("distribution visibility=%v: %s", fork, body)
		}
		if !strings.Contains(body, version.String()) {
			t.Fatal("upstream version lost")
		}
		if fork {
			for _, want := range []string{"&lt;Fork&gt;", "2.1", "&lt;Supplier&gt;"} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Contains(body, "<Fork>") || strings.Contains(body, "<Supplier>") {
				t.Fatal("unsafe distribution metadata")
			}
		}
	}
}

func renderCfgAdminAbout(t *testing.T, b *Base) string {
	t.Helper()
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	if err := store.save([]*Base{b}); err != nil {
		t.Fatalf("store.save: %v", err)
	}

	req := httptest.NewRequest("GET", "/bases/"+b.ID+"/configurator/admin/about", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", b.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	(&handler{store: store}).cfgAdminAbout(rec, req)
	if rec.Code != 200 {
		t.Fatalf("cfgAdminAbout status=%d body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestCfgAdminAbout_ShowsSQLiteAndFileConfigLocations(t *testing.T) {
	b := &Base{
		ID:           "about-file",
		Name:         "File base",
		ConfigSource: "file",
		Path:         `/srv/configs/<trade>`,
		DBType:       "sqlite",
		DBPath:       `/srv/data/<trade>.db`,
		Port:         8080,
	}
	html := renderCfgAdminAbout(t, b)

	for _, want := range []string{
		"Расположение конфигурации",
		"Файлы",
		`/srv/configs/&lt;trade&gt;`,
		"База данных",
		`/srv/data/&lt;trade&gt;.db`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в окне «О программе» нет %q", want)
		}
	}
	if strings.Contains(html, "<trade>") {
		t.Error("пути в окне «О программе» должны быть HTML-экранированы")
	}
}

func TestCfgAdminAbout_DatabaseConfigShowsItsSQLiteFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "application.db")
	b := &Base{
		ID:           "about-database",
		Name:         "Database base",
		ConfigSource: "database",
		DBType:       "sqlite",
		DBPath:       dbPath,
		Port:         8081,
	}
	html := renderCfgAdminAbout(t, b)

	if !strings.Contains(html, "В базе данных") {
		t.Error("не показан режим хранения конфигурации в базе данных")
	}
	if strings.Count(html, dbPath) != 2 {
		t.Errorf("путь SQLite должен быть указан и для конфигурации, и для данных; HTML: %s", html)
	}
}

func TestCfgAdminAbout_MasksPostgresPassword(t *testing.T) {
	b := &Base{
		ID:           "about-postgres",
		Name:         "Postgres base",
		ConfigSource: "file",
		Path:         "/srv/onebase/config",
		DBType:       "postgres",
		DB:           "postgres://onebase:very-secret@db.example/production",
		Port:         8082,
	}
	html := renderCfgAdminAbout(t, b)

	if !strings.Contains(html, "postgres://onebase:***@db.example/production") {
		t.Error("в окне «О программе» нет маскированного PostgreSQL DSN")
	}
	if strings.Contains(html, "very-secret") {
		t.Error("окно «О программе» раскрыло пароль PostgreSQL")
	}
}
