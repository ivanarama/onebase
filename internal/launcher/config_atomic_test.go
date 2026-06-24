package launcher

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
)

// TestSaveConfigFiles_FileMode: пакетная запись пишет все файлы и не оставляет
// .tmp-остатков (признак атомарной записи temp+rename) — чтобы --watch не увидел
// частичный/пустой файл между записями страницы.
func TestSaveConfigFiles_FileMode(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	b := &Base{Name: "Тест", ConfigSource: "file", Path: dir}
	if err := s.Add(b); err != nil {
		t.Fatalf("Add: %v", err)
	}
	h := &handler{store: s, runner: NewRunner()}
	req := httptest.NewRequest("POST", "/", nil)

	files := []configFileEntry{
		{relPath: "src/Сводка.page.os", content: []byte("Процедура ПриФормировании()")},
		{relPath: "pages/Сводка.yaml", content: []byte("name: Сводка")},
	}
	wantContent := map[string][]byte{
		"src/Сводка.page.os": []byte("Процедура ПриФормировании()"),
		"pages/Сводка.yaml":  []byte("name: Сводка\n"),
	}
	if err := saveConfigFiles(req, h, b, files); err != nil {
		t.Fatalf("saveConfigFiles: %v", err)
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.relPath)))
		if err != nil {
			t.Fatalf("read %s: %v", f.relPath, err)
		}
		if !bytes.Equal(got, wantContent[f.relPath]) {
			t.Fatalf("%s: содержимое %q", f.relPath, got)
		}
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".tmp") {
			t.Fatalf("остался временный файл: %s", p)
		}
		return nil
	})
}

// TestSaveConfigFiles_DBMode: в database-режиме оба файла коммитятся одной
// транзакцией — после вызова обе записи присутствуют в _onebase_config.
func TestSaveConfigFiles_DBMode(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t)
	b := &Base{Name: "ТестБД", ConfigSource: "database", DBType: "sqlite", DBPath: filepath.Join(dir, "cfg.db")}
	if err := s.Add(b); err != nil {
		t.Fatalf("Add: %v", err)
	}
	h := &handler{store: s, runner: NewRunner()}
	req := httptest.NewRequest("POST", "/", nil)

	files := []configFileEntry{
		{relPath: "src/Сводка.page.os", content: []byte("src")},
		{relPath: "pages/Сводка.yaml", content: []byte("name: Сводка")},
	}
	wantContent := map[string][]byte{
		"src/Сводка.page.os": []byte("src"),
		"pages/Сводка.yaml":  []byte("name: Сводка\n"),
	}
	if err := saveConfigFiles(req, h, b, files); err != nil {
		t.Fatalf("saveConfigFiles(db): %v", err)
	}

	db, err := OpenDB(context.Background(), b)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	repo := configdb.New(db)
	for _, f := range files {
		content, ok, err := repo.ReadFile(context.Background(), f.relPath)
		if err != nil || !ok {
			t.Fatalf("ReadFile %s: ok=%v err=%v", f.relPath, ok, err)
		}
		if !bytes.Equal(content, wantContent[f.relPath]) {
			t.Fatalf("%s: содержимое %q", f.relPath, content)
		}
	}
}
