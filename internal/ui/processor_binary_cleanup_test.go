package ui

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise both Cleanup callers through mounted HTTP routes. The successful
// form-event case also proves multipart binary input is supported; obFire's
// URL-encoded binary transport remains covered by the existing refusal tests.
func TestProcessorBinaryParam_TempFileCleanupHTTP(t *testing.T) {
	for _, mode := range []string{"desktop", "demo"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "demo" {
				previous := interpreter.FileSandboxRoot()
				interpreter.SetFileSandbox(t.TempDir())
				t.Cleanup(func() { interpreter.SetFileSandbox(previous) })
			}
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				for _, route := range []string{"run", "form-event"} {
					t.Run(route, func(t *testing.T) {
						for _, scenario := range []string{"success", "corrupt-book", "second-create", "second-write", "second-close"} {
							t.Run(scenario, func(t *testing.T) {
								testProcessorBinaryCleanupHTTP(t, db, mode, route, scenario)
							})
						}
					})
				}
			})
		})
	}
}

func testProcessorBinaryCleanupHTTP(t *testing.T, db *storage.DB, mode, route, scenario string) {
	t.Helper()
	s, ctx, unusedServer, cat := binaryImportFixture(t, db)
	unusedServer.Close()
	// A dialect's scenarios share its schema, so remove rows from prior success
	// controls before checking that a failed request created none.
	if _, err := db.Exec(ctx, `DELETE FROM "ученики"`); err != nil {
		t.Fatal(err)
	}
	proc := s.reg.GetProcessor("ЗагрузкаУчеников")
	secondFailure := strings.HasPrefix(scenario, "second-")
	if secondFailure {
		proc.Params = append(proc.Params, processor.Param{Name: "ВторойФайл", Type: "binary"})
	}
	if route == "form-event" {
		form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"})
		for _, p := range proc.Params {
			form.Elements = append(form.Elements, &metadata.FormElement{
				Kind: metadata.FormElementField, Name: "Поле" + p.Name,
				DataPath: "Объект." + p.Name, Type: "file",
			})
		}
		proc.Forms = []*metadata.FormModule{form}
	}

	data := xlsxWithStudents(t)
	if scenario == "corrupt-book" {
		data = []byte("this is not an xlsx zip archive")
	}
	tempDir := t.TempDir()
	if mode == "demo" {
		// Preserve the production sandbox constraint without changing TMPDIR.
		tempDir = filepath.Join(interpreter.FileSandboxRoot(), "cleanup-case")
		if err := os.MkdirAll(tempDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var paths []string
	var calls int
	var firstSaved bool
	const failureCause = "controlled second binary upload failure"
	s.createProcessorTempFile = func(_ string, pattern string) (processorUploadFile, error) {
		calls++
		if calls == 2 {
			// Positive control at the failure boundary: the first upload must be
			// fully written, closed and still present when the second starts.
			stored, err := os.ReadFile(paths[0])
			if err != nil || !bytes.Equal(stored, data) || !firstSaved {
				return nil, fmt.Errorf("first upload not saved before second: %v", err)
			}
			if scenario == "second-create" {
				return nil, fmt.Errorf("%s", failureCause)
			}
		}
		f, err := os.CreateTemp(tempDir, pattern)
		if err != nil {
			return nil, err
		}
		paths = append(paths, f.Name())
		if calls == 2 && secondFailure {
			return &failingProcessorUpload{File: f, stage: strings.TrimPrefix(scenario, "second-"), cause: fmt.Errorf("%s", failureCause)}, nil
		}
		return &observedProcessorUpload{File: f, expected: data, saved: &firstSaved}, nil
	}

	router := chi.NewRouter()
	s.Mount(router)
	finished := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		router.ServeHTTP(w, r)
	}))
	defer ts.Close()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, p := range proc.Params {
		part, err := writer.CreateFormFile(p.Name, p.Name+".xlsx")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	target := ts.URL + "/ui/processor/" + url.PathEscape(proc.Name)
	if route == "form-event" {
		target += "/form-event"
		for key, value := range map[string]string{"_element": "Выполнить", "_event": string(metadata.FormEventOnClick)} {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, target, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept-Language", "ru")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	// EOF alone is not the proof: wait until the mounted handler and all its
	// deferred cleanups have returned before reading the factory observations.
	<-finished
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close response: %v / %v", readErr, closeErr)
	}
	wantCalls, wantPaths := 1, 1
	if secondFailure {
		wantCalls = 2
		if scenario != "second-create" {
			wantPaths = 2
		}
	}
	if calls != wantCalls || len(paths) != wantPaths || !firstSaved {
		t.Fatalf("upload path not reached: calls=%d paths=%v firstSaved=%v; response=%s", calls, paths, firstSaved, page)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("temporary upload %q survived handler (err=%v)", path, err)
		}
		if mode == "demo" {
			if _, err := interpreter.ResolveSafePath(path); err != nil {
				t.Errorf("upload escaped demo sandbox: %v", err)
			}
		}
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil || len(entries) != 0 {
		t.Errorf("upload directory not empty: entries=%v err=%v", entries, err)
	}
	wantStatus := http.StatusOK
	if secondFailure {
		wantStatus = http.StatusBadRequest
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("status=%d, want %d: %s", resp.StatusCode, wantStatus, page)
	}
	failure := string(page)
	if route == "form-event" {
		result := decodeFormEventResponse(t, page)
		if result.OK != (scenario == "success") {
			t.Fatalf("unexpected form-event result: %+v", result)
		}
		failure = result.Error
		if secondFailure && len(result.Messages) != 0 {
			t.Fatalf("DSL ran before uploads completed: %+v", result)
		}
	}
	if scenario == "corrupt-book" && (!strings.Contains(failure, "ПрочитатьExcel") || !strings.Contains(failure, "zip: not a valid zip file")) {
		t.Fatalf("did not reach corrupt workbook reader: %s", page)
	}
	if secondFailure && (!strings.Contains(failure, "временный файл параметра ВторойФайл") || !strings.Contains(failure, failureCause) || strings.Contains(string(page), "путь: ")) {
		t.Fatalf("did not stop on second upload failure: %s", page)
	}
	rows, err := s.store.List(ctx, cat.Name, cat, storage.ListParams{RowFilterEvaluated: true})
	if err != nil {
		t.Fatal(err)
	}
	wantRows := 0
	if scenario == "success" {
		wantRows = 2
	}
	if len(rows) != wantRows {
		t.Fatalf("catalog rows=%d, want %d: %s", len(rows), wantRows, page)
	}
}

type observedProcessorUpload struct {
	*os.File
	expected []byte
	saved    *bool
}

func (f *observedProcessorUpload) Close() error {
	if err := f.File.Close(); err != nil {
		return err
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		return err
	}
	*f.saved = bytes.Equal(data, f.expected)
	return nil
}
