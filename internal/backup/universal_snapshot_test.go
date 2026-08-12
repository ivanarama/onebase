package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
)

// triggerWriter вызывает fire ровно один раз — на первой записи в архив: так
// конкурентная запись попадает ровно в середину выгрузки.
type triggerWriter struct {
	buf  bytes.Buffer
	once sync.Once
	fire func()
}

func (w *triggerWriter) Write(p []byte) (int, error) {
	w.once.Do(w.fire)
	return w.buf.Write(p)
}

// Архив обязан быть согласованным: объект и его история попадают в него из
// одного состояния базы. Без общего снимка выгрузка собирает таблицы
// последовательными запросами, и запись, случившаяся между ними, оставляет в
// архиве объект на прежнем этапе с уже новой историей — состояние, которого
// никогда не существовало.
func TestExportUniversal_SnapshotKeepsObjectAndHistoryConsistent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "consistency.db")

	src, err := storage.ConnectSQLite(ctx, path)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(src.Close)
	src.SetFilesDir(filepath.Join(dir, "files"))
	if err := src.EnsureStageHistorySchema(ctx); err != nil {
		t.Fatalf("EnsureStageHistorySchema: %v", err)
	}
	if _, err := src.Exec(ctx, `CREATE TABLE orders (id TEXT PRIMARY KEY, stage TEXT, payload TEXT)`); err != nil {
		t.Fatal(err)
	}
	const target = "11111111-1111-1111-1111-111111111111"
	if _, err := src.Exec(ctx,
		`INSERT INTO orders(id,stage,payload) VALUES(?,?,?)`, target, "Черновик", "x"); err != nil {
		t.Fatal(err)
	}
	// Балласт: zip.Writer буферизует через bufio(4096), без объёма первая запись
	// в w случится только на Close — и врезка уже ничего не покажет.
	for i := 0; i < 400; i++ {
		if _, err := src.Exec(ctx, `INSERT INTO orders(id,stage,payload) VALUES(?,?,?)`,
			fmt.Sprintf("2222%04d-1111-1111-1111-111111111111", i), "Черновик",
			strings.Repeat(fmt.Sprintf("%08x", i*2654435761), 8)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := src.Exec(ctx,
		`INSERT INTO _stage_history(id,entity_name,record_id,field,field_id,event_no,from_stage,to_stage,at,source)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		"33333333-3333-3333-3333-333333333333", "Заявка", target, "Состояние", "f1",
		1, "", "Черновик", "2026-08-13 10:00:00.000", "local"); err != nil {
		t.Fatal(err)
	}

	// Пишущее соединение — ОТДЕЛЬНОЕ: SetMaxOpenConns(1) означает, что запись
	// через тот же handle встанет насмерть за соединением, которое держит
	// снимок выгрузки.
	writerDB, err := storage.ConnectSQLite(ctx, path)
	if err != nil {
		t.Fatalf("ConnectSQLite(writer): %v", err)
	}
	t.Cleanup(writerDB.Close)

	fired := false
	w := &triggerWriter{fire: func() {
		fired = true
		if _, err := writerDB.Exec(ctx, `UPDATE orders SET stage=? WHERE id=?`, "Утверждена", target); err != nil {
			t.Errorf("concurrent update: %v", err)
		}
		if _, err := writerDB.Exec(ctx,
			`INSERT INTO _stage_history(id,entity_name,record_id,field,field_id,event_no,from_stage,to_stage,at,source)
			 VALUES(?,?,?,?,?,?,?,?,?,?)`,
			"44444444-4444-4444-4444-444444444444", "Заявка", target, "Состояние", "f1",
			2, "Черновик", "Утверждена", "2026-08-13 10:05:00.000", "local"); err != nil {
			t.Errorf("concurrent history insert: %v", err)
		}
	}}

	if err := ExportUniversal(ctx, src, "file", testConfigDir(t), "", "consistency", w); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}
	if !fired {
		t.Fatal("врезка не сработала: архив не писался в w до конца выгрузки")
	}

	archived := w.buf.Bytes()
	stage := archiveOrderStage(t, archived, target)
	events := archiveHistoryEvents(t, archived, target)
	t.Logf("stage in archive = %q, history events = %d", stage, events)
	if stage == "Черновик" && events != 1 {
		t.Fatalf("несогласованный архив: объект на этапе %q, а событий в истории %d", stage, events)
	}
	if stage == "Утверждена" && events != 2 {
		t.Fatalf("несогласованный архив: объект на этапе %q, а событий в истории %d", stage, events)
	}
}

func archiveEntry(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rc.Close() }()
		body, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	t.Fatalf("в архиве нет %s", name)
	return nil
}

func archiveOrderStage(t *testing.T, data []byte, id string) string {
	t.Helper()
	for _, line := range strings.Split(string(archiveEntry(t, data, "data/orders.jsonl")), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("jsonl: %v", err)
		}
		if row["id"] == id {
			s, _ := row["stage"].(string)
			return s
		}
	}
	t.Fatalf("в архиве нет объекта %s", id)
	return ""
}

func archiveHistoryEvents(t *testing.T, data []byte, id string) int {
	t.Helper()
	count := 0
	for _, line := range strings.Split(string(archiveEntry(t, data, "system/_stage_history.jsonl")), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("jsonl: %v", err)
		}
		if row["record_id"] == id {
			count++
		}
	}
	return count
}
