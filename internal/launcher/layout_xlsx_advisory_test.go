package launcher

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/testutil/xlsxadvisory"
)

func TestImportXLSX_PublicAdvisories(t *testing.T) {
	for _, scenario := range xlsxadvisory.Cases {
		t.Run(scenario, func(t *testing.T) {
			xlsxadvisory.Run(t, func() {
				h, b, dir := newLayoutTestBase(t)
				// The configurator's auth middleware needs a real base DB.
				// Keep it under the fixture, rather than the launcher's default
				// persistent database path in os.TempDir().
				b.DBType = "sqlite"
				b.DBPath = filepath.Join(dir, "base.db")
				if err := h.store.Update(b); err != nil {
					t.Fatal(err)
				}

				srv, err := NewServer(h.store, h.runner)
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- srv.ListenAndServe() }()
				defer func() { srv.Close(); <-done }()
				var body bytes.Buffer
				mw := multipart.NewWriter(&body)
				for k, v := range map[string]string{"name": "Проверка", "document": "Реализация"} {
					if err := mw.WriteField(k, v); err != nil {
						t.Fatal(err)
					}
				}
				part, err := mw.CreateFormFile("file", "advisory.xlsx")
				if err != nil {
					t.Fatal(err)
				}
				data := xlsxadvisory.Book(t, scenario)
				if _, err := part.Write(data); err != nil {
					t.Fatal(err)
				}
				if err := mw.Close(); err != nil {
					t.Fatal(err)
				}
				req, err := http.NewRequest(http.MethodPost, srv.URL()+"/bases/"+b.ID+"/configurator/layout/import-xlsx", &body)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", mw.FormDataContentType())
				req.Header.Set("Accept-Language", "ru")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				page, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				if err := resp.Body.Close(); err != nil {
					t.Fatal(err)
				}
				if scenario == "cyclic-array" {
					if resp.StatusCode != 200 {
						t.Fatalf("cached formulas: %d %s", resp.StatusCode, page)
					}
					saved, err := os.ReadFile(filepath.Join(dir, "printforms", "Проверка.template.xlsx"))
					if err != nil || !bytes.Equal(saved, data) {
						t.Fatalf("template bytes changed: %v", err)
					}
				} else {
					if !strings.Contains(string(page), `class="err-box">Не удалось прочитать файл Excel`) || strings.Contains(string(page), "runtime error") {
						t.Fatalf("wrong rejection: %d %s", resp.StatusCode, truncate(string(page), 2000))
					}
					for _, ext := range []string{".layout.yaml", ".template.xlsx"} {
						if _, err := os.Stat(filepath.Join(dir, "printforms", "Проверка"+ext)); !os.IsNotExist(err) {
							t.Fatalf("partial layout saved: %s (%v)", ext, err)
						}
					}
				}
			})
		})
	}
}
