package ui

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/storage"
)

// Faults use a per-server file factory, so they work on Windows and under root
// without relying on chmod, resource limits, or a writable /dev/full.
type failingProcessorUpload struct {
	*os.File
	stage string
	cause error
}

func (f *failingProcessorUpload) Write(p []byte) (int, error) {
	if f.stage == "write" {
		return 0, f.cause
	}
	return f.File.Write(p)
}

func (f *failingProcessorUpload) Close() error {
	err := f.File.Close()
	if f.stage == "close" {
		return f.cause
	}
	return err
}

func failingProcessorTempFactory(stage string, cause error) func(string, string) (processorUploadFile, error) {
	return func(dir, pattern string) (processorUploadFile, error) {
		if stage == "create" {
			return nil, cause
		}
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		return &failingProcessorUpload{File: f, stage: stage, cause: cause}, nil
	}
}

func TestProcessorBinaryParam_StorageFailureLocalized(t *testing.T) {
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"directory", "create", "write", "close"} {
		for _, lang := range []string{"en", "ru"} {
			t.Run(stage+"/"+lang, func(t *testing.T) {
				s, ctx, ts, cat := binaryImportFixture(t)
				s.cfg.Bundle = bundle
				data := xlsxWithStudents(t)
				tempDir := t.TempDir()
				cause := &os.PathError{Op: stage, Path: "upload-test", Err: os.ErrPermission}
				if stage == "directory" {
					tempDir = filepath.Join(tempDir, "not-a-directory")
					if err := os.WriteFile(tempDir, []byte("file"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					s.createProcessorTempFile = failingProcessorTempFactory(stage, cause)
				}
				// os.TempDir uses TMPDIR on Unix and TMP/TEMP on Windows.
				for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
					t.Setenv(key, tempDir)
				}
				code, page := postBinaryProcessor(t, ts, "ЗагрузкаУчеников", "Файл", "students.xlsx", data, lang)
				if code != http.StatusBadRequest {
					t.Fatalf("status %d, want 400: %s", code, page)
				}
				want := "temporary file for parameter Файл: "
				if stage == "directory" {
					want = "temporary upload directory: "
				}
				if lang == "ru" {
					want = "временный файл параметра Файл: "
					if stage == "directory" {
						want = "временный каталог загрузок: "
					}
				}
				if !strings.HasPrefix(page, want) {
					t.Fatalf("want prefix %q: %s", want, page)
				}
				if stage == "directory" {
					if !strings.Contains(page, "not-a-directory") || strings.TrimSpace(page) == strings.TrimSpace(want) {
						t.Fatalf("lost directory failure cause: %s", page)
					}
				} else if !strings.Contains(page, cause.Error()) {
					t.Fatalf("lost file failure cause: %s", page)
				}
				rows, err := s.store.List(ctx, cat.Name, cat, storage.ListParams{RowFilterEvaluated: true})
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) != 0 || strings.Contains(page, "путь: ") {
					t.Fatalf("DSL continued after upload failure: %s; rows=%v", page, rows)
				}
			})
		}
	}
}

// The HTTP regression above proves routing and localization. These checks also
// verify the structured cause, which HTTP cannot expose through errors.Is/As.
func TestProcessorBinaryParam_StorageFailurePreservesCause(t *testing.T) {
	for _, stage := range []string{"create", "write", "close"} {
		t.Run(stage, func(t *testing.T) {
			tempDir := t.TempDir()
			for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(key, tempDir)
			}
			cause := &os.PathError{Op: stage, Path: "upload-test", Err: os.ErrPermission}
			temps := &processorTempFiles{createTemp: failingProcessorTempFactory(stage, cause)}
			defer temps.Cleanup()
			_, err := temps.save("Файл", "students.xlsx", []byte("test"))
			var localized *i18nerr.Error
			var pathErr *os.PathError
			if !errors.Is(err, os.ErrPermission) || !errors.As(err, &pathErr) || pathErr != cause || !errors.As(err, &localized) {
				t.Fatalf("lost wrapped cause/type: %T %v", err, err)
			}
		})
	}
}
