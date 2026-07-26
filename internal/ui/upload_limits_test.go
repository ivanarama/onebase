package ui

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadUploadedBytesRejectsOversizedFile(t *testing.T) {
	file := &memoryMultipartFile{Reader: bytes.NewReader([]byte("12345"))}
	if _, err := readUploadedBytes(file, 4); !errors.Is(err, errUploadTooLarge) {
		t.Fatalf("error = %v, want errUploadTooLarge", err)
	}
}

func TestParseBoundedFormReportsRequestEntityTooLarge(t *testing.T) {
	s := &Server{maxFileSizeBytes: 4}
	body := "field=" + strings.Repeat("x", int(uiMultipartOverhead)+5)
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.limitMultipartRequest(w, r)

	err := parseBoundedForm(r, 32<<20)
	if status := uploadErrorStatus(err); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("error = %v, status = %d", err, status)
	}
}

type memoryMultipartFile struct {
	*bytes.Reader
}

func (f *memoryMultipartFile) Close() error { return nil }
