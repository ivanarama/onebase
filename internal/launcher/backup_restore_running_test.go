package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
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
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO t(name) VALUES('beta')"); err != nil {
		t.Fatalf("insert beta: %v", err)
	}
	db.Close()

	resp := postRestore(t, h, b, file)
	if resp["ok"] != true {
		t.Fatalf("восстановление остановленной базы не выполнено: %v", resp)
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

// Полное восстановление из .obz устроено так же и точно так же не видело
// усыновлённую базу: universal-ветка перезаливает таблицы в БД под работающим
// приложением, бинарная — подменяет файл (issue #627, те же две строки).
func TestBackupFullImport_AdoptedRunningBaseRefused(t *testing.T) {
	for _, tc := range []struct{ name, format string }{
		{"universal", "universal"},
		{"binary", "binary"},
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
