package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"archive/zip"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/storage"
)

// adoptedBase поднимает живую базу так, как её видит лаунчер после перезапуска:
// на порту отвечает /health, но процесса в runner.procs нет («усыновление»,
// см. baseRunning). Возвращает базу с заполненным файлом SQLite.
func adoptedBase(t *testing.T, id string, alive bool) (*handler, *Base, string) {
	t.Helper()
	ctx := context.Background()
	projDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "live.db")

	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	if _, err := db.Exec(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO t(name) VALUES('alpha')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()

	port := freePort(t)
	if alive {
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Fatalf("занять порт базы: %v", err)
		}
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		// Порт базы фиксирован в реестре — слушатель на нём подменяем своим.
		if err := srv.Listener.Close(); err != nil {
			t.Fatalf("закрыть слушатель httptest: %v", err)
		}
		srv.Listener = ln
		srv.Start()
		t.Cleanup(srv.Close)
	}

	store := newTestStore(t)
	b := &Base{
		ID: id, Name: "Тест восстановления", ConfigSource: "file",
		Path: projDir, DBType: "sqlite", DBPath: dbPath, Port: port,
	}
	if err := store.Add(b); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	return &handler{store: store, runner: NewRunner()}, b, dbPath
}

// stubExePath не даёт обработчику запустить «платформу» дочерним процессом:
// под `go test` os.Executable() — это тест-бинарь, и его запуск прогоняет весь
// пакет заново (рекурсивно). Нужен там, где путь восстановления доходит до
// MigrateBase — то есть в проверке, что до него как раз не доходит.
func stubExePath(t *testing.T) {
	t.Helper()
	prev := exePath
	missing := filepath.Join(t.TempDir(), "onebase-not-here")
	exePath = func() (string, error) { return missing, nil }
	t.Cleanup(func() { exePath = prev })
}

// makeBackup снимает копию базы в каталог копий этой базы и возвращает имя файла.
func makeBackup(t *testing.T, h *handler, b *Base) string {
	t.Helper()
	out, err := backup.DumpSQLite(context.Background(), b.DBPath, h.backupDir(b))
	if err != nil {
		t.Fatalf("DumpSQLite: %v", err)
	}
	return filepath.Base(out)
}

func postRestore(t *testing.T, h *handler, b *Base, file string) map[string]any {
	t.Helper()
	req := httptest.NewRequest("POST", "/bases/"+b.ID+"/configurator/backup/"+file+"/restore", nil)
	req.Header.Set("X-Onebase-Ajax", "1")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", b.ID)
	rctx.URLParams.Add("file", file)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.backupRestore(rec, req)

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, rec.Body.String())
	}
	return resp
}

type fullImportTestEntry struct {
	name string
	data []byte
}

func postFullImport(t *testing.T, h *handler, b *Base, entries []fullImportTestEntry) map[string]any {
	t.Helper()
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for _, entry := range entries {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatalf("zip %s: %v", entry.name, err)
		}
		if _, err := w.Write(entry.data); err != nil {
			t.Fatalf("запись %s: %v", entry.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("закрытие архива: %v", err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("obz_file", "full.obz")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(archive.Bytes()); err != nil {
		t.Fatalf("запись формы: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("закрытие формы: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/bases/"+b.ID+"/configurator/backup/full-import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Onebase-Ajax", "1")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", b.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.backupFullImport(rec, req)

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, rec.Body.String())
	}
	return resp
}

func TestBackupFullImport_UnreadableSQLiteIsNotRenamedBeforeRestoreJournal(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "corrupt.db")
	original := []byte("this is not a sqlite database")
	if err := os.WriteFile(dbPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	store := newTestStore(t)
	b := &Base{
		ID: "corrupt-universal-restore", Name: "Corrupt", ConfigSource: "file",
		Path: t.TempDir(), DBType: "sqlite", DBPath: dbPath, Port: freePort(t),
	}
	if err := store.Add(b); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}

	resp := postFullImport(t, h, b, []fullImportTestEntry{
		{name: "META.txt", data: []byte("onebase_full_export\nformat=universal\ndb_type=sqlite\n")},
		{name: "manifest.json", data: []byte("{}")},
		{name: "config/app.yaml", data: []byte("name: restored\n")},
	})
	if resp["ok"] == true {
		t.Fatalf("restore unexpectedly succeeded against an unreadable SQLite file: %v", resp)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read original database after refusal: %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("unreadable SQLite bytes changed: got %q, want %q", after, original)
	}
	if _, err := os.Stat(dbPath + ".old"); !os.IsNotExist(err) {
		t.Fatalf("unsafe pre-journal .old file exists: %v", err)
	}
	if !h.runner.lifecycleMu.TryLock() {
		t.Fatal("failed restore leaked lifecycle gate")
	}
	h.runner.lifecycleMu.Unlock()
}

func TestEnsureBaseStoppedForRestoreRejectsStaleBaseRecord(t *testing.T) {
	h, stale, _ := adoptedBase(t, "stale-restore", false)
	current := *stale
	current.DBPath = filepath.Join(t.TempDir(), "replacement.db")
	if err := h.store.Update(&current); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/bases/"+stale.ID+"/configurator/backup/full-import", nil)
	req.Header.Set("X-Onebase-Ajax", "1")
	rec := httptest.NewRecorder()
	_, _, release, ok := h.ensureBaseStoppedForRestore(rec, req, stale, "ru")
	if ok || release != nil {
		if release != nil {
			release()
		}
		t.Fatal("stale restore target was accepted")
	}
	if !strings.Contains(rec.Body.String(), "измен") {
		t.Fatalf("response does not explain stale target: %s", rec.Body.String())
	}
	if !h.runner.lifecycleMu.TryLock() {
		t.Fatal("stale-target rejection leaked lifecycle gate")
	}
	h.runner.lifecycleMu.Unlock()
}

// Восстановление поверх «усыновлённой» живой базы обязано отказать: её процесса
// у лаунчера нет, остановить он её не может, а подмена файла БД под работающим
// процессом уносит незачекпойнченный WAL (issue #627). Раньше живость
// проверялась через runner.IsRunning, который усыновление не видит, — защита не
// срабатывала вовсе.
func TestBackupRestore_AdoptedRunningBaseRefused(t *testing.T) {
	h, b, dbPath := adoptedBase(t, "adopted-restore", true)
	file := makeBackup(t, h, b)

	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("чтение файла БД: %v", err)
	}

	resp := postRestore(t, h, b, file)
	if resp["ok"] == true {
		t.Fatalf("восстановление выполнено поверх работающей базы: %v", resp)
	}
	errText, _ := resp["error"].(string)
	if !strings.Contains(errText, "не этим лаунчером") {
		t.Errorf("ожидался отказ «база запущена не этим лаунчером», получено: %q", errText)
	}

	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("чтение файла БД: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("файл работающей базы перезаписан")
	}
	if _, err := os.Stat(dbPath + ".old"); err == nil {
		t.Error("создана копия .old — восстановление дошло до подмены файла")
	}
}

// Контроль: остановленная база (порт свободен) восстанавливается как прежде —
// защита не должна запирать нормальный сценарий.
func TestBackupRestore_StoppedBaseRestores(t *testing.T) {
	h, b, dbPath := adoptedBase(t, "stopped-restore", false)
	file := makeBackup(t, h, b)

	// Меняем живую базу после снятия копии: успешное восстановление обязано
	// вернуть её к состоянию копии.
	ctx := context.Background()
	// Configurator auth normally keeps this pool cached for the launcher
	// lifetime. Restore must evict and close it before replacing SQLite.
	db, err := getAuthDB(ctx, b)
	if err != nil {
		t.Fatalf("getAuthDB: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO t(name) VALUES('beta')"); err != nil {
		t.Fatalf("insert beta: %v", err)
	}

	resp := postRestore(t, h, b, file)
	if resp["ok"] != true {
		t.Fatalf("восстановление остановленной базы не выполнено: %v", resp)
	}
	if _, cached := cfgAuthDBs.Load(b.ID); cached {
		t.Fatal("restore оставил кэшированный pool к подменённому SQLite-файлу")
	}

	db, err = storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite после восстановления: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("после восстановления строк %d, ожидалась 1 (состояние копии)", n)
	}
}

func TestRestoreLeaseBlocksConcurrentStartUntilReleased(t *testing.T) {
	h, b, _ := adoptedBase(t, "restore-lease", false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/restore", nil)
	_, _, release, ok := h.ensureBaseStoppedForRestore(rec, req, b, "ru")
	if !ok {
		t.Fatalf("failed to acquire restore lease: %s", rec.Body.String())
	}
	if err := h.runner.Start(b); err == nil || !strings.Contains(err.Error(), "временно запрещён") {
		release()
		t.Fatalf("concurrent Start was not blocked by restore lease: %v", err)
	}
	release()
	if err := h.runner.holdStarts(); err != nil {
		t.Fatalf("restore lease was not released: %v", err)
	}
	h.runner.AllowStarts()
}

func TestRestoreFailsClosedForOccupiedUnresponsiveBasePort(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	t.Cleanup(ts.Close)
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	b := &Base{ID: "unknown-port", Name: "Unknown", Port: port}
	store := newTestStore(t)
	if err := store.Add(b); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}
	rec := httptest.NewRecorder()
	_, _, _, ok := h.ensureBaseStoppedForRestore(rec,
		httptest.NewRequest(http.MethodPost, "/restore", nil), b, "ru")
	if ok {
		t.Fatal("restore was allowed while the configured port was occupied but identity/health failed")
	}
	if portFree(port) {
		t.Fatal("unknown listener was killed by port")
	}
}

// Полное восстановление из .obz устроено так же и точно так же не видело
// усыновлённую базу: universal-ветка перезаливает таблицы в БД под работающим
// приложением, бинарная — подменяет файл (issue #627, те же две строки).
func TestBackupFullImport_AdoptedRunningBaseRefused(t *testing.T) {
	for _, tc := range []struct{ name, format string }{
		{"universal", "universal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubExePath(t)
			h, b, dbPath := adoptedBase(t, "adopted-obz-"+tc.name, true)

			var archive bytes.Buffer
			zw := zip.NewWriter(&archive)
			meta, err := zw.Create("META.txt")
			if err != nil {
				t.Fatalf("zip META.txt: %v", err)
			}
			if _, err := meta.Write([]byte("onebase_full_export\nformat=" + tc.format + "\ndb_type=sqlite\n")); err != nil {
				t.Fatalf("запись META.txt: %v", err)
			}
			dbEntry, err := zw.Create("database.db")
			if err != nil {
				t.Fatalf("zip database.db: %v", err)
			}
			dump, err := os.ReadFile(dbPath)
			if err != nil {
				t.Fatalf("чтение файла БД: %v", err)
			}
			if _, err := dbEntry.Write(dump); err != nil {
				t.Fatalf("запись database.db: %v", err)
			}
			configEntry, err := zw.Create("config/app.yaml")
			if err != nil {
				t.Fatalf("zip config/app.yaml: %v", err)
			}
			if _, err := configEntry.Write([]byte("name: Test\n")); err != nil {
				t.Fatalf("запись config/app.yaml: %v", err)
			}
			if tc.format == "universal" {
				manifest, err := zw.Create("manifest.json")
				if err != nil {
					t.Fatalf("zip manifest.json: %v", err)
				}
				if _, err := manifest.Write([]byte("{}")); err != nil {
					t.Fatalf("запись manifest.json: %v", err)
				}
			}
			if err := zw.Close(); err != nil {
				t.Fatalf("закрытие архива: %v", err)
			}

			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			part, err := mw.CreateFormFile("obz_file", "full.obz")
			if err != nil {
				t.Fatalf("CreateFormFile: %v", err)
			}
			if _, err := part.Write(archive.Bytes()); err != nil {
				t.Fatalf("запись формы: %v", err)
			}
			if err := mw.Close(); err != nil {
				t.Fatalf("закрытие формы: %v", err)
			}

			before, err := os.ReadFile(dbPath)
			if err != nil {
				t.Fatalf("чтение файла БД: %v", err)
			}

			req := httptest.NewRequest("POST", "/bases/"+b.ID+"/configurator/backup/full-import", &body)
			req.Header.Set("Content-Type", mw.FormDataContentType())
			req.Header.Set("X-Onebase-Ajax", "1")
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", b.ID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()
			h.backupFullImport(rec, req)

			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("ответ не JSON: %v (%s)", err, rec.Body.String())
			}
			if resp["ok"] == true {
				t.Fatalf("полное восстановление выполнено поверх работающей базы: %v", resp)
			}
			errText, _ := resp["error"].(string)
			if !strings.Contains(errText, "не этим лаунчером") {
				t.Errorf("ожидался отказ «база запущена не этим лаунчером», получено: %q", errText)
			}

			after, err := os.ReadFile(dbPath)
			if err != nil {
				t.Fatalf("чтение файла БД: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Error("файл работающей базы перезаписан")
			}
		})
	}
}

func TestBackupFullImport_IncompleteArchiveRejectedBeforeStopping(t *testing.T) {
	tests := []struct {
		name    string
		entries func([]byte) []fullImportTestEntry
		wantErr string
	}{
		{
			name: "complete legacy binary snapshot",
			entries: func(db []byte) []fullImportTestEntry {
				return []fullImportTestEntry{
					{name: "META.txt", data: []byte("onebase_full_export\nformat=binary\ndb_type=sqlite\n")},
					{name: "database.db", data: db},
					{name: "config/app.yaml", data: []byte("name: Test\n")},
				}
			},
			wantErr: "старый бинарный",
		},
		{
			name: "binary missing database payload",
			entries: func([]byte) []fullImportTestEntry {
				return []fullImportTestEntry{
					{name: "META.txt", data: []byte("onebase_full_export\nformat=binary\ndb_type=sqlite\n")},
					{name: "config/app.yaml", data: []byte("name: Test\n")},
				}
			},
			wantErr: "дамп базы данных",
		},
		{
			name: "binary has only config directory",
			entries: func(db []byte) []fullImportTestEntry {
				return []fullImportTestEntry{
					{name: "META.txt", data: []byte("onebase_full_export\nformat=binary\ndb_type=sqlite\n")},
					{name: "database.db", data: db},
					{name: "config/"},
				}
			},
			wantErr: "файла конфигурации",
		},
		{
			name: "binary config entry escapes config directory",
			entries: func(db []byte) []fullImportTestEntry {
				return []fullImportTestEntry{
					{name: "META.txt", data: []byte("onebase_full_export\nformat=binary\ndb_type=sqlite\n")},
					{name: "database.db", data: db},
					{name: "config/../not-config.yaml", data: []byte("name: NotConfig\n")},
				}
			},
			wantErr: "файла конфигурации",
		},
		{
			name: "universal missing manifest",
			entries: func([]byte) []fullImportTestEntry {
				return []fullImportTestEntry{
					{name: "META.txt", data: []byte("onebase_full_export\nformat=universal\n")},
					{name: "config/app.yaml", data: []byte("name: Test\n")},
				}
			},
			wantErr: "manifest.json",
		},
		{
			name: "universal missing config",
			entries: func([]byte) []fullImportTestEntry {
				return []fullImportTestEntry{
					{name: "META.txt", data: []byte("onebase_full_export\nformat=universal\n")},
					{name: "manifest.json", data: []byte("{}")},
				}
			},
			wantErr: "файла конфигурации",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, b, dbPath := adoptedBase(t, "incomplete-obz-"+strings.ReplaceAll(tc.name, " ", "-"), true)
			db, err := os.ReadFile(dbPath)
			if err != nil {
				t.Fatalf("чтение файла БД: %v", err)
			}

			resp := postFullImport(t, h, b, tc.entries(db))
			if resp["ok"] == true {
				t.Fatalf("неполный архив принят: %v", resp)
			}
			errText, _ := resp["error"].(string)
			if !strings.Contains(errText, tc.wantErr) {
				t.Fatalf("ожидалась ошибка %q до проверки живой базы, получено: %q", tc.wantErr, errText)
			}
			if portFree(b.Port) {
				t.Fatal("живую базу остановили до проверки полноты архива")
			}
		})
	}
}
