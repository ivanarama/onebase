package launcher

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestRenderConfigHistory_EscapesAndShowsDiff(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	versions := []configdb.Version{
		{ID: "22222222-2222-2222-2222-222222222222", CreatedAt: now, Message: "<b>bad</b>"},
		{ID: "11111111-1111-1111-1111-111111111111", CreatedAt: now.Add(-time.Hour), Message: "old"},
	}
	diff := []configdb.DiffEntry{{
		Path:   "config/app.yaml",
		Kind:   configdb.DiffModified,
		Before: []byte("name: <old>\n"),
		After:  []byte("name: <new>\n"),
	}}
	out := renderConfigHistory("base", versions, versions[1].ID, versions[0].ID, diff, nil)
	if !strings.Contains(out, "История конфигурации") || !strings.Contains(out, "cfgHistoryRollback") {
		t.Fatalf("history UI missing key controls: %s", out)
	}
	if !strings.Contains(out, "cfgHistoryCompare") || !strings.Contains(out, "export-zip") || !strings.Contains(out, "export-obz") {
		t.Fatalf("history UI missing compare/export controls: %s", out)
	}
	if strings.Contains(out, "<b>bad</b>") || strings.Contains(out, "name: <old>") {
		t.Fatalf("history UI did not escape HTML: %s", out)
	}
	if !strings.Contains(out, "&lt;b&gt;bad&lt;/b&gt;") || !strings.Contains(out, "config/app.yaml") || !strings.Contains(out, "modified") {
		t.Fatalf("history UI missing escaped data/diff: %s", out)
	}
}

func TestCfgAdminConfigHistoryRollback_RestoresSnapshot(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "base.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()

	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := repo.SaveFile(ctx, "config/app.yaml", []byte("name: v1\n")); err != nil {
		t.Fatalf("SaveFile v1: %v", err)
	}
	versions, err := repo.ListVersions(ctx, 10)
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions after v1: len=%d err=%v", len(versions), err)
	}
	baseline := versions[0]
	if err := repo.SaveFile(ctx, "config/app.yaml", []byte("name: v2\n")); err != nil {
		t.Fatalf("SaveFile v2: %v", err)
	}

	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{ID: "history-rollback", Name: "History", ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}
	if err := store.save([]*Base{base}); err != nil {
		t.Fatalf("store.save: %v", err)
	}
	t.Cleanup(CloseAuthPools)
	h := &handler{store: store}

	req := httptest.NewRequest("POST", "/bases/history-rollback/configurator/admin/config-history/rollback", strings.NewReader(`{"id":"`+baseline.ID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", base.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.cfgAdminConfigHistoryRollback(w, req)
	if w.Code != 200 {
		t.Fatalf("rollback status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK || resp.ID == "" {
		t.Fatalf("bad rollback response: %+v", resp)
	}
	content, ok, err := repo.ReadFile(ctx, "config/app.yaml")
	if err != nil || !ok || string(content) != "name: v1\n" {
		t.Fatalf("rolled back content ok=%v err=%v content=%q", ok, err, string(content))
	}
	versions, err = repo.ListVersions(ctx, 10)
	if err != nil {
		t.Fatalf("ListVersions after rollback: %v", err)
	}
	if len(versions) != 3 || !strings.HasPrefix(versions[0].Message, "rollback to ") {
		t.Fatalf("rollback version not recorded: %+v", versions)
	}
}

func TestCfgAdminConfigHistoryExportZip(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "base.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := repo.SaveFiles(ctx, []configdb.ConfigFile{
		{Path: "config/app.yaml", Content: []byte("name: zip\n")},
		{Path: "src/main.os", Content: []byte("Процедура X()\nКонецПроцедуры\n")},
	}, configdb.VersionOptions{Message: "zip seed"}); err != nil {
		t.Fatalf("SaveFiles: %v", err)
	}
	versions, err := repo.ListVersions(ctx, 1)
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions len=%d err=%v", len(versions), err)
	}

	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{ID: "history-export", Name: "History", ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}
	if err := store.save([]*Base{base}); err != nil {
		t.Fatalf("store.save: %v", err)
	}
	t.Cleanup(CloseAuthPools)
	h := &handler{store: store}

	req := httptest.NewRequest("GET", "/bases/history-export/configurator/admin/config-history/"+versions[0].ID+"/export-zip", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", base.ID)
	rctx.URLParams.Add("version", versions[0].ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.cfgAdminConfigHistoryExportZip(w, req)
	if w.Code != 200 {
		t.Fatalf("export status=%d body=%s", w.Code, w.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("zip reader: %v", err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip file %s: %v", f.Name, err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(data)
	}
	if got["config/app.yaml"] != "name: zip\n" || !strings.Contains(got["src/main.os"], "Процедура X") {
		t.Fatalf("bad zip contents: %+v", got)
	}

	req = httptest.NewRequest("GET", "/bases/history-export/configurator/admin/config-history/"+versions[0].ID+"/export-obz", nil)
	rctx = chi.NewRouteContext()
	rctx.URLParams.Add("id", base.ID)
	rctx.URLParams.Add("version", versions[0].ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w = httptest.NewRecorder()

	h.cfgAdminConfigHistoryExportOBZ(w, req)
	if w.Code != 200 {
		t.Fatalf("obz export status=%d body=%s", w.Code, w.Body.String())
	}
	zr, err = zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("obz reader: %v", err)
	}
	got = map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open obz file %s: %v", f.Name, err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(data)
	}
	if got["config/config/app.yaml"] != "name: zip\n" || !strings.Contains(got["config/src/main.os"], "Процедура X") {
		t.Fatalf("bad obz config contents: %+v", got)
	}
	if !strings.Contains(got["META.txt"], "format=universal") || !strings.Contains(got["META.txt"], "source_config_version="+versions[0].ID) {
		t.Fatalf("bad obz meta: %q", got["META.txt"])
	}
}
