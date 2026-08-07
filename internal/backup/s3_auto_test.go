package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// fakeStore is an in-memory ObjectStore for exercising the S3 upload/rotation
// branch of createAutoBackup without touching the network.
type fakeStore struct {
	puts    map[string][]byte
	deleted []string
	putErr  error
}

func newFakeStore() *fakeStore { return &fakeStore{puts: map[string][]byte{}} }

func (f *fakeStore) PutObject(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	if f.putErr != nil {
		return f.putErr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.puts[key] = b
	return nil
}

func (f *fakeStore) ListKeys(_ context.Context, prefix string) ([]string, error) {
	var keys []string
	for k := range f.puts {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (f *fakeStore) DeleteObject(_ context.Context, key string) error {
	delete(f.puts, key)
	f.deleted = append(f.deleted, key)
	return nil
}

func staticDumper(name, content string) autoDumper {
	return func(_ context.Context, _ AutoTarget, outDir string) (string, error) {
		path := filepath.Join(outDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", err
		}
		return path, nil
	}
}

func TestCreateAutoBackup_UploadsToS3(t *testing.T) {
	dir := t.TempDir()
	cfg := &project.BackupConfig{
		Directory: dir,
		S3:        &project.S3Config{Bucket: "b", Prefix: "prod/"},
	}
	store := newFakeStore()

	_, err := createAutoBackup(context.Background(), cfg, AutoTarget{},
		staticDumper("backup_new.db", "payload"),
		func(*project.S3Config) (ObjectStore, error) { return store, nil })
	if err != nil {
		t.Fatalf("createAutoBackup: %v", err)
	}
	got, ok := store.puts["prod/backup_new.db"]
	if !ok {
		t.Fatalf("expected object prod/backup_new.db, have keys %v", keysOf(store))
	}
	if string(got) != "payload" {
		t.Errorf("uploaded content = %q, want payload", got)
	}
}

func TestCreateAutoBackup_RotatesS3(t *testing.T) {
	dir := t.TempDir()
	cfg := &project.BackupConfig{
		Directory: dir,
		S3:        &project.S3Config{Bucket: "b", Prefix: "prod/", KeepLast: 2},
	}
	store := newFakeStore()
	// Pre-existing objects (older) plus a non-backup file that must be ignored.
	store.puts["prod/backup_2026-01-01.db"] = []byte("1")
	store.puts["prod/backup_2026-01-02.db"] = []byte("2")
	store.puts["prod/backup_2026-01-03.db"] = []byte("3")
	store.puts["prod/notes.txt"] = []byte("keep me")

	_, err := createAutoBackup(context.Background(), cfg, AutoTarget{},
		staticDumper("backup_2026-01-04.db", "4"),
		func(*project.S3Config) (ObjectStore, error) { return store, nil })
	if err != nil {
		t.Fatalf("createAutoBackup: %v", err)
	}

	// KeepLast=2 → newest two (04, 03) remain; 01 and 02 deleted; notes.txt kept.
	for _, want := range []string{"prod/backup_2026-01-04.db", "prod/backup_2026-01-03.db", "prod/notes.txt"} {
		if _, ok := store.puts[want]; !ok {
			t.Errorf("expected %s to survive rotation; keys=%v", want, keysOf(store))
		}
	}
	for _, gone := range []string{"prod/backup_2026-01-01.db", "prod/backup_2026-01-02.db"} {
		if _, ok := store.puts[gone]; ok {
			t.Errorf("expected %s to be rotated away", gone)
		}
	}
}

func TestCreateAutoBackup_S3ErrorKeepsLocal(t *testing.T) {
	dir := t.TempDir()
	cfg := &project.BackupConfig{
		Directory: dir,
		S3:        &project.S3Config{Bucket: "b", Prefix: "prod/"},
	}
	store := newFakeStore()
	store.putErr = errors.New("network down")

	path, err := createAutoBackup(context.Background(), cfg, AutoTarget{},
		staticDumper("backup_new.db", "payload"),
		func(*project.S3Config) (ObjectStore, error) { return store, nil })
	if err == nil || !strings.Contains(err.Error(), "s3 upload") {
		t.Fatalf("expected s3 upload error, got %v", err)
	}
	// Local backup must remain despite the S3 failure.
	if filepath.Base(path) != "backup_new.db" {
		t.Fatalf("path = %s", path)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("local backup should survive S3 failure: %v", statErr)
	}
}

func TestS3Key(t *testing.T) {
	cases := map[string][2]string{
		"prod/backup_x.db": {"prod/", "backup_x.db"},
		"prod/backup_y.db": {"prod", "backup_y.db"},
		"backup_z.db":      {"", "backup_z.db"},
	}
	for want, in := range cases {
		if got := s3Key(in[0], in[1]); got != want {
			t.Errorf("s3Key(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func keysOf(f *fakeStore) []string {
	var ks []string
	for k := range f.puts {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Приёмка ровно по формулировке issue #628: две базы пишут в один префикс,
// keep_last=2 — у КАЖДОЙ обязано остаться по два своих объекта, посторонний файл
// в бакете не тронут.
//
// Раньше отбор шёл по расширению, а сортировка была лексической, и первыми
// «свежими» вставали все backup_zeta_* (z > a), из-за чего в хвост попадали ВСЕ
// копии alpha, включая только что загруженную: off-site копии базы не оставалось
// вовсе, а джоб рапортовал успех.
func TestCreateAutoBackup_RotatesOnlyOwnObjects(t *testing.T) {
	store := newFakeStore()
	for _, k := range []string{
		"prod/backup_zeta_2026-08-01.db",
		"prod/backup_zeta_2026-08-02.db",
		"prod/backup_zeta_2026-08-03.db",
		"prod/backup_alpha_2026-08-01.db",
		"prod/backup_alpha_2026-08-02.db",
		"prod/archive-2019.sql.gz", // положен руками, ротации не принадлежит
	} {
		store.puts[k] = []byte("x")
	}
	mkStore := func(*project.S3Config) (ObjectStore, error) { return store, nil }

	// Каждая база — со своим локальным каталогом: общий каталог перемешал бы
	// файлы двух баз и в локальной ротации, а проверяем мы off-site.
	for _, fresh := range []string{"backup_alpha_2026-08-03.db", "backup_zeta_2026-08-04.db"} {
		cfg := &project.BackupConfig{
			Directory: t.TempDir(),
			S3:        &project.S3Config{Bucket: "b", Prefix: "prod/", KeepLast: 2},
		}
		if _, err := createAutoBackup(context.Background(), cfg, AutoTarget{},
			staticDumper(fresh, "fresh"), mkStore); err != nil {
			t.Fatalf("createAutoBackup(%s): %v", fresh, err)
		}
	}

	survive := []string{
		"prod/backup_alpha_2026-08-03.db", // свежая alpha
		"prod/backup_alpha_2026-08-02.db", // вторая из keep_last=2
		"prod/backup_zeta_2026-08-04.db",  // свежая zeta
		"prod/backup_zeta_2026-08-03.db",
		"prod/archive-2019.sql.gz", // посторонний объект
	}
	for _, want := range survive {
		if _, ok := store.puts[want]; !ok {
			t.Errorf("объект %s удалён ротацией; осталось: %v", want, keysOf(store))
		}
	}
	for _, gone := range []string{
		"prod/backup_alpha_2026-08-01.db",
		"prod/backup_zeta_2026-08-01.db",
		"prod/backup_zeta_2026-08-02.db",
	} {
		if _, ok := store.puts[gone]; ok {
			t.Errorf("устаревшая копия %s не удалена; осталось: %v", gone, keysOf(store))
		}
	}
	// Считаем именно количество по семейству: проверка «выжил/не выжил» не ловит
	// недоудаление, ради которого keep_last и задают.
	for family, want := range map[string]int{"backup_alpha_": 2, "backup_zeta_": 2} {
		got := 0
		for _, k := range keysOf(store) {
			if strings.HasPrefix(path.Base(k), family) {
				got++
			}
		}
		if got != want {
			t.Errorf("копий %s* осталось %d, ожидалось %d; осталось: %v", family, got, want, keysOf(store))
		}
	}
}

// Объекты во вложенных «папках» префикса принадлежат чужой инсталляции: своя
// копия всегда лежит непосредственно под префиксом (s3Key склеивает префикс и
// имя файла). Совпадение семейства при этом ничего не значит — имя базы может
// совпасть у двух установок.
func TestCreateAutoBackup_RotationSkipsNestedKeys(t *testing.T) {
	cfg := &project.BackupConfig{
		Directory: t.TempDir(),
		S3:        &project.S3Config{Bucket: "b", Prefix: "prod/", KeepLast: 1},
	}
	store := newFakeStore()
	store.puts["prod/other-install/backup_alpha_2026-08-01.db"] = []byte("чужая инсталляция")
	store.puts["prod/backup_alpha_2026-08-01.db"] = []byte("своя старая")

	if _, err := createAutoBackup(context.Background(), cfg, AutoTarget{},
		staticDumper("backup_alpha_2026-08-02.db", "fresh"),
		func(*project.S3Config) (ObjectStore, error) { return store, nil }); err != nil {
		t.Fatalf("createAutoBackup: %v", err)
	}
	if _, ok := store.puts["prod/other-install/backup_alpha_2026-08-01.db"]; !ok {
		t.Errorf("копия чужой инсталляции удалена ротацией; осталось: %v", keysOf(store))
	}
	if _, ok := store.puts["prod/backup_alpha_2026-08-02.db"]; !ok {
		// Лексически «prod/other-install/…» идёт после «prod/backup_…», поэтому
		// прежняя ротация оставляла чужой объект и сносила свою свежую копию.
		t.Errorf("свежая копия удалена ротацией; осталось: %v", keysOf(store))
	}
	if _, ok := store.puts["prod/backup_alpha_2026-08-01.db"]; ok {
		t.Errorf("своя старая копия не удалена; осталось: %v", keysOf(store))
	}
}

// Без prefix ListKeys отдаёт весь бакет. Посторонние объекты с «бэкапным»
// расширением ротация трогать не должна (issue #628, дефект 1).
func TestCreateAutoBackup_RotationSkipsForeignObjectsWithoutPrefix(t *testing.T) {
	dir := t.TempDir()
	cfg := &project.BackupConfig{
		Directory: dir,
		S3:        &project.S3Config{Bucket: "b", KeepLast: 1},
	}
	store := newFakeStore()
	store.puts["archive-2019.sql.gz"] = []byte("чужой архив")
	store.puts["backup_alpha_2026-08-01.db"] = []byte("своя старая")

	_, err := createAutoBackup(context.Background(), cfg, AutoTarget{},
		staticDumper("backup_alpha_2026-08-02.db", "fresh"),
		func(*project.S3Config) (ObjectStore, error) { return store, nil })
	if err != nil {
		t.Fatalf("createAutoBackup: %v", err)
	}
	if _, ok := store.puts["archive-2019.sql.gz"]; !ok {
		t.Errorf("посторонний объект удалён ротацией; осталось: %v", keysOf(store))
	}
	if _, ok := store.puts["backup_alpha_2026-08-02.db"]; !ok {
		t.Errorf("свежая копия удалена ротацией; осталось: %v", keysOf(store))
	}
	if _, ok := store.puts["backup_alpha_2026-08-01.db"]; ok {
		t.Errorf("старая копия своей базы не удалена; осталось: %v", keysOf(store))
	}
}

// Ротация не должна удалять только что загруженную копию ни при каком порядке
// разбора имён: иначе off-site копии базы не остаётся вовсе.
func TestCreateAutoBackup_RotationKeepsFreshObject(t *testing.T) {
	dir := t.TempDir()
	cfg := &project.BackupConfig{
		Directory: dir,
		S3:        &project.S3Config{Bucket: "b", Prefix: "prod/", KeepLast: 1},
	}
	store := newFakeStore()
	// Копия «из будущего»: по метке она новее свежей и займёт единственное место.
	store.puts["prod/backup_alpha_2027-01-01.db"] = []byte("из будущего")

	_, err := createAutoBackup(context.Background(), cfg, AutoTarget{},
		staticDumper("backup_alpha_2026-08-02.db", "fresh"),
		func(*project.S3Config) (ObjectStore, error) { return store, nil })
	if err != nil {
		t.Fatalf("createAutoBackup: %v", err)
	}
	if _, ok := store.puts["prod/backup_alpha_2026-08-02.db"]; !ok {
		t.Errorf("свежая копия удалена ротацией; осталось: %v", keysOf(store))
	}
}

// Разбор имени: семейство базы и метка времени в обоих форматах, которыми
// платформа их пишет (наносекунды у SQLite, секунды у PostgreSQL). Имя базы с
// подчёркиванием разбирается с хвоста, а не по первому разделителю.
func TestSplitBackupName(t *testing.T) {
	cases := []struct {
		name       string
		wantFamily string
		wantStamp  string // "" — метка не разобрана
	}{
		{"backup_trade_2026-08-07_12-30-05.000000000.db", "backup_trade_", "2026-08-07T12:30:05Z"},
		{"prod/backup_trade_2026-08-07_12-30-05.sql.gz", "backup_trade_", "2026-08-07T12:30:05Z"},
		{"backup_my_db_2026-08-07.db", "backup_my_db_", "2026-08-07T00:00:00Z"},
		// Метка без секунд — так писал sqlite.go до перехода на наносекунды;
		// такие файлы могли остаться в каталогах бэкапов и в бакетах.
		{"backup_mydb_2026-05-06_14-30.db", "backup_mydb_", "2026-05-06T14:30:00Z"},
		{"archive-2019.sql.gz", "", ""},
		{"backup_trade.db", "", ""},
		{"notes.txt", "", ""},
	}
	for _, tc := range cases {
		family, stamp, ok := splitBackupName(tc.name)
		if tc.wantFamily == "" {
			if ok {
				t.Errorf("splitBackupName(%q): имя разобрано (%q), а не должно", tc.name, family)
			}
			continue
		}
		if !ok {
			t.Errorf("splitBackupName(%q): имя не разобрано", tc.name)
			continue
		}
		if family != tc.wantFamily {
			t.Errorf("splitBackupName(%q): семейство %q, ожидалось %q", tc.name, family, tc.wantFamily)
		}
		if got := stamp.UTC().Format(time.RFC3339); got != tc.wantStamp {
			t.Errorf("splitBackupName(%q): метка %s, ожидалась %s", tc.name, got, tc.wantStamp)
		}
	}
}

// Парсер имени и генератор имени обязаны сходиться. Если они разъедутся,
// rotateS3 уйдёт в ветку «имя не по шаблону»: предупреждение в журнал, return
// nil — ротация тихо выключена, а джоб зелёный. Поэтому имя берём не из
// литерала, а у публичной DumpSQLite. Имя базы с «_» — заодно проверка, что
// разбор идёт с хвоста, а не по первому разделителю.
func TestSplitBackupName_РазбираетИмяОтDumpSQLite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "my_db.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	if _, err := db.Exec(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	db.Close()

	out, err := DumpSQLite(ctx, dbPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("DumpSQLite: %v", err)
	}
	family, stamp, ok := splitBackupName(filepath.Base(out))
	if !ok {
		t.Fatalf("splitBackupName(%q): имя копии не разобрано — ротация off-site копий тихо отключится", filepath.Base(out))
	}
	if family != "backup_my_db_" {
		t.Errorf("семейство = %q, ожидалось %q", family, "backup_my_db_")
	}
	// Метка в имени пишется по локальным часам, а time.Parse без зоны отдаёт UTC,
	// поэтому сравниваем с текущим временем, пропущенным через тот же формат:
	// сама метка — ярлык для относительного порядка копий, а не момент времени.
	now, err := time.Parse(backupStampLayouts[0], time.Now().Format(backupStampLayouts[0]))
	if err != nil {
		t.Fatalf("нормализация текущего времени: %v", err)
	}
	if d := now.Sub(stamp); d < 0 || d > time.Hour {
		t.Errorf("метка времени = %s, ожидалась близкая к текущей (%s)", stamp, now)
	}
}
