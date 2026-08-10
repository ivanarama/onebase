package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/scheduler"
)

func TestRegisterAutoBackup_Defaults(t *testing.T) {
	sched := scheduler.New(nil, nil, nil)
	cfg := &project.BackupConfig{Enabled: true}

	if err := RegisterAutoBackup(cfg, AutoTarget{ProjectDir: t.TempDir()}, sched); err != nil {
		t.Fatalf("RegisterAutoBackup: %v", err)
	}
	jobs := sched.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs len = %d, want 1", len(jobs))
	}
	if jobs[0].Name != "AutoBackup" || jobs[0].Schedule != defaultAutoBackupSchedule {
		t.Fatalf("job = %+v", jobs[0])
	}
}

func TestRegisterAutoBackup_DisabledNoop(t *testing.T) {
	sched := scheduler.New(nil, nil, nil)
	if err := RegisterAutoBackup(&project.BackupConfig{}, AutoTarget{}, sched); err != nil {
		t.Fatalf("RegisterAutoBackup disabled: %v", err)
	}
	if len(sched.Jobs()) != 0 {
		t.Fatalf("disabled config registered jobs: %+v", sched.Jobs())
	}
}

func TestCreateAutoBackup_RotatesWithDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg := &project.BackupConfig{Directory: dir}
	// Имена — те, что платформа реально генерирует (backup_<БД>_<дата>):
	// раньше здесь стояли backup_old_a.sql.gz и backup_new.sql.gz, которых не
	// бывает, и тест не проверял отбор по семейству вовсе.
	for i := 0; i < defaultAutoBackupKeepLast; i++ {
		name := fmt.Sprintf("backup_trade_2026-08-%02d_03-00-00.sql.gz", i+1)
		touchBackup(t, dir, name, time.Now().Add(-time.Duration(i+10)*time.Hour))
	}

	created, err := createAutoBackup(context.Background(), cfg, AutoTarget{}, func(_ context.Context, _ AutoTarget, outDir string) (string, error) {
		path := filepath.Join(outDir, "backup_trade_2026-08-20_03-00-00.sql.gz")
		if err := os.WriteFile(path, []byte("ok"), 0o644); err != nil {
			return "", err
		}
		now := time.Now()
		if err := os.Chtimes(path, now, now); err != nil {
			return "", err
		}
		return path, nil
	}, nil)
	if err != nil {
		t.Fatalf("createAutoBackup: %v", err)
	}
	if filepath.Base(created) != "backup_trade_2026-08-20_03-00-00.sql.gz" {
		t.Fatalf("created = %s", created)
	}
	files, err := BackupFiles(dir)
	if err != nil {
		t.Fatalf("BackupFiles: %v", err)
	}
	if len(files) != defaultAutoBackupKeepLast {
		t.Fatalf("files len = %d, want %d", len(files), defaultAutoBackupKeepLast)
	}
	if filepath.Base(files[0].Path) != "backup_trade_2026-08-20_03-00-00.sql.gz" {
		t.Fatalf("newest = %s", files[0].Path)
	}
}

// Приёмка по issue #673: две базы пишут в ОДИН каталог (docs/DEPLOYMENT.md
// показывает фиксированный абсолютный путь, и при двух базах на хосте он общий),
// keep_last=2 — у каждой обязано остаться по две своих копии.
//
// Раньше отбор шёл одной маской backup_*, поэтому ротация одной базы сносила
// копии соседней: при невезучем расписании у базы не оставалось ни одной.
func TestRotateBackups_KeepsPerDatabase(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i, name := range []string{
		"backup_alpha_2026-08-01_03-00-00.sql.gz",
		"backup_alpha_2026-08-02_03-00-00.sql.gz",
		"backup_alpha_2026-08-03_03-00-00.sql.gz",
		"backup_zeta_2026-08-01_09-00-00.db",
		"backup_zeta_2026-08-02_09-00-00.db",
		"backup_zeta_2026-08-03_09-00-00.db",
	} {
		touchBackup(t, dir, name, now.Add(-time.Duration(len([]string{})+i)*time.Hour))
	}
	// Файл backup_* с неразбираемым именем: чей он — неизвестно, трогать нельзя.
	touchBackup(t, dir, "backup_вручную.sql.gz", now.Add(-100*time.Hour))

	if err := RotateBackups(dir, 2); err != nil {
		t.Fatalf("RotateBackups: %v", err)
	}

	files, err := BackupFiles(dir)
	if err != nil {
		t.Fatalf("BackupFiles: %v", err)
	}
	count := map[string]int{}
	for _, f := range files {
		base := filepath.Base(f.Path)
		switch {
		case strings.HasPrefix(base, "backup_alpha_"):
			count["alpha"]++
		case strings.HasPrefix(base, "backup_zeta_"):
			count["zeta"]++
		default:
			count["прочее"]++
		}
	}
	if count["alpha"] != 2 {
		t.Errorf("копий alpha осталось %d, ожидалось 2; в каталоге: %v", count["alpha"], namesOf(files))
	}
	if count["zeta"] != 2 {
		t.Errorf("копий zeta осталось %d, ожидалось 2; в каталоге: %v", count["zeta"], namesOf(files))
	}
	if count["прочее"] != 1 {
		t.Errorf("посторонний файл удалён ротацией; в каталоге: %v", namesOf(files))
	}
}

func namesOf(files []FileInfo) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, filepath.Base(f.Path))
	}
	sort.Strings(out)
	return out
}

func TestBackupFiles_KnownExtensionsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	old := touchBackup(t, dir, "backup_a.sql.gz", time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC))
	newer := touchBackup(t, dir, "backup_b.db", time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC))
	_ = touchBackup(t, dir, "notes.txt", time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))

	files, err := BackupFiles(dir)
	if err != nil {
		t.Fatalf("BackupFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}
	if files[0].Path != newer || files[1].Path != old {
		t.Fatalf("unexpected order: %+v", files)
	}
}

func touchBackup(t *testing.T, dir, name string, ts time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
	return path
}
