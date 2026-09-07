package launcher

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/storage"
)

// Публичные PDF/XLSX handlers доходят до атомарного create-if-absent в обоих
// режимах хранения. Барьер ставится перед обращением к хранилищу: для файлов
// он детерминированно воспроизводит прежнее окно Stat -> WriteFile, а SQL-
// атомарность отдельно проверяет TestCreateFileConcurrentMatrix.
func TestLayoutImportsConcurrentCreate(t *testing.T) {
	for _, storageMode := range []string{"file", "database"} {
		for _, importKind := range []string{"pdf", "xlsx"} {
			t.Run(storageMode+"/"+importKind, func(t *testing.T) {
				testConcurrentLayoutImport(t, storageMode, importKind)
			})
		}
	}
}

func testConcurrentLayoutImport(t *testing.T, storageMode, importKind string) {
	t.Helper()
	var (
		h   *handler
		b   *Base
		dir string
	)
	if storageMode == "database" {
		h, b = newLayoutTestBaseDB(t)
		db, err := storage.ConnectSQLite(context.Background(), b.DBPath)
		if err != nil {
			t.Fatalf("ConnectSQLite: %v", err)
		}
		t.Cleanup(db.Close)
		repo := configdb.New(db)
		h.createConfigFile = func(ctx context.Context, _ *Base, relPath string, src []byte) error {
			return repo.CreateFile(ctx, relPath, src)
		}
	} else {
		h, b, dir = newLayoutTestBase(t)
	}

	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	h.beforeLayoutCreate = func() {
		arrived <- struct{}{}
		<-release
	}

	const name = "Конкурентный"
	responses := make(chan *httptest.ResponseRecorder, 2)
	pdfData := syntheticTablePDF(t)
	xlsxData := blankXLSX(t)
	post := func(document string) {
		if importKind == "pdf" {
			responses <- postImportPDF(t, h, b, name, document, "1", pdfData)
			return
		}
		responses <- postImportXLSX(t, h, b, name, document, "", xlsxData)
	}
	go post("ДокументПервый")
	go post("ДокументВторой")

	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			close(release)
			t.Fatal("handlers did not reach atomic layout create")
		}
	}
	close(release)

	var exists, created int
	for i := 0; i < 2; i++ {
		select {
		case rec := <-responses:
			body := rec.Body.String()
			if strings.Contains(body, "уже существует") || strings.Contains(body, "already exists") {
				exists++
			} else if strings.Contains(body, "создан из") || strings.Contains(body, "created from") {
				created++
			} else {
				t.Fatalf("unexpected response: %s", truncate(body, 1000))
			}
		case <-time.After(15 * time.Second):
			t.Fatal("concurrent layout handler did not finish")
		}
	}
	if exists != 1 || created != 1 {
		t.Fatalf("responses: created=%d exists=%d, want 1/1", created, exists)
	}

	relPath := "printforms/" + name + ".layout.yaml"
	var (
		content []byte
		ok      bool
	)
	if storageMode == "database" {
		content, ok = configReadLayout(t, b, relPath)
	} else {
		var err error
		content, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(relPath)))
		if err != nil {
			t.Fatalf("read layout: %v", err)
		}
		ok = true
	}
	if !ok {
		t.Fatal("layout was not created")
	}
	parsed, err := printform.ParseLayoutBytes(content)
	if err != nil {
		t.Fatalf("stored layout is partial: %v", err)
	}
	if parsed.Document != "ДокументПервый" && parsed.Document != "ДокументВторой" {
		t.Fatalf("stored document = %q", parsed.Document)
	}
}
