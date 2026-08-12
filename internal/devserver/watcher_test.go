package devserver

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatchContextStopsAndSignalsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done, err := WatchContext(ctx, t.TempDir(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop after context cancellation")
	}
}

func startWatcher(t *testing.T, dir string, onChange func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done, err := WatchContext(ctx, dir, onChange)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("watcher did not stop during cleanup")
		}
	})
}

// WatchContext должен вызывать onChange при изменении файла.
func TestWatchContext_TriggersOnFileChange(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "x.os")
	if err := os.WriteFile(file, []byte("// initial"), 0o644); err != nil {
		t.Fatal(err)
	}

	var changes int32
	startWatcher(t, dir, func() { atomic.AddInt32(&changes, 1) })

	// debounce — 300ms. Запишем файл и подождём.
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(file, []byte("// edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Watcher debounce = 300ms; ждём 500ms.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&changes) > 0 {
			return // OK
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("onChange не вызвался после редактирования файла")
}

// Watch должен ловить правки в подкаталогах (.os-модули лежат в src/),
// а не только в корне проекта — fsnotify не рекурсивен сам по себе.
func TestWatchContext_TriggersOnSubdirChange(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "поступление.proc.os")
	if err := os.WriteFile(file, []byte("// initial"), 0o644); err != nil {
		t.Fatal(err)
	}

	var changes int32
	startWatcher(t, dir, func() { atomic.AddInt32(&changes, 1) })

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(file, []byte("// edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&changes) > 0 {
			return // OK
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("onChange не вызвался после редактирования файла в подкаталоге")
}

// Подкаталог, созданный уже после старта Watch, тоже должен отслеживаться.
func TestWatchContext_TriggersOnNewSubdir(t *testing.T) {
	dir := t.TempDir()

	var changes int32
	startWatcher(t, dir, func() { atomic.AddInt32(&changes, 1) })

	time.Sleep(50 * time.Millisecond)
	sub := filepath.Join(dir, "documents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Дать watcher'у время добавить новый каталог в наблюдение.
	time.Sleep(400 * time.Millisecond)
	atomic.StoreInt32(&changes, 0)
	if err := os.WriteFile(filepath.Join(sub, "счёт.yaml"), []byte("name: Счёт"), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&changes) > 0 {
			return // OK
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("onChange не вызвался для файла в созданном после старта каталоге")
}

func TestWatchContext_Debounces(t *testing.T) {
	// Раньше серия правок растягивалась на 250 мс при штатном окне 300 мс —
	// запас всего шестикратный, и на нагруженном раннере одна пауза выходила за
	// окно: таймер срабатывал посреди серии, тест видел два вызова вместо одного.
	// Теперь окно шире, а правки плотнее: 80 мс серии против 1 с окна.
	// Проверяемое поведение (N правок → один onChange) не меняется.
	prev := debounceWindow
	debounceWindow = time.Second
	t.Cleanup(func() { debounceWindow = prev })

	dir := t.TempDir()
	file := filepath.Join(dir, "y.os")
	if err := os.WriteFile(file, []byte("// initial"), 0o644); err != nil {
		t.Fatal(err)
	}

	var changes int32
	startWatcher(t, dir, func() { atomic.AddInt32(&changes, 1) })

	time.Sleep(50 * time.Millisecond)
	// Несколько правок подряд должны схлопнуться в один onChange.
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(file, []byte("// edited"+string(rune('A'+i))), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Ждём срабатывания, а не фиксированной паузы: так тест не зависит от того,
	// насколько быстро раннер донёс события fsnotify.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&changes) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	// Дать шанс лишним вызовам проявиться — иначе тест не отличит «схлопнулось»
	// от «второй вызов ещё не пришёл».
	time.Sleep(debounceWindow)

	got := atomic.LoadInt32(&changes)
	if got != 1 {
		t.Errorf("ожидался 1 onChange (debounce), получили %d", got)
	}
}

func TestWatchContextRejectsMissingDirectory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := WatchContext(ctx, filepath.Join(t.TempDir(), "missing"), func() {}); err == nil {
		t.Fatal("expected missing directory error")
	}
}

func TestWatchContextRecoversCallbackPanic(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "x.os")
	if err := os.WriteFile(file, []byte("// initial"), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	startWatcher(t, dir, func() {
		if calls.Add(1) == 1 {
			panic("boom")
		}
	})

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(file, []byte("// first"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && calls.Load() < 1 {
		time.Sleep(20 * time.Millisecond)
	}
	if err := os.WriteFile(file, []byte("// second"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("watcher stopped after callback panic")
}

func TestWatchProjectContextIgnoresGeneratedFiles(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	var changes atomic.Int32
	done, err := WatchProjectContext(ctx, dir, func() { changes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		<-done
	}()

	time.Sleep(50 * time.Millisecond)
	backupDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "backup.sqlite"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if got := changes.Load(); got != 0 {
		t.Fatalf("generated backup triggered %d reloads", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "document.yaml"), []byte("name: Test"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if changes.Load() == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("project file did not trigger reload; calls = %d", changes.Load())
}

func TestWatchProjectContextTracksMetadataDirectoryRename(t *testing.T) {
	dir := t.TempDir()
	documents := filepath.Join(dir, "documents")
	if err := os.MkdirAll(documents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(documents, "invoice.yaml"), []byte("name: Invoice"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var changes atomic.Int32
	done, err := WatchProjectContext(ctx, dir, func() { changes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		<-done
	}()

	time.Sleep(50 * time.Millisecond)
	if err := os.Rename(documents, filepath.Join(dir, "archived-documents")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if changes.Load() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("renaming a watched metadata directory did not trigger reload")
}

// WatchGoContext реагирует на правку .go и молчит на всё остальное: конфигурацию
// в dev-режиме перезагружает отдельный наблюдатель, пересобирать ради неё бинарь
// незачем.
func TestWatchGoContext_OnlyGoSources(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var changes atomic.Int32
	done, err := WatchGoContext(ctx, dir, func() { changes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		<-done
	}()

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "документ.yaml"), []byte("name: Счёт"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "проведение.os"), []byte("// код"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if got := changes.Load(); got != 0 {
		t.Fatalf("конфигурация вызвала пересборку %d раз", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main // правка"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if changes.Load() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("правка .go не вызвала onChange")
}

// go.mod и go.sum — тоже вход сборки: смена зависимости меняет бинарь.
func TestWatchGoContext_TracksGoMod(t *testing.T) {
	dir := t.TempDir()
	modPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(modPath, []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var changes atomic.Int32
	done, err := WatchGoContext(ctx, dir, func() { changes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		<-done
	}()

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(modPath, []byte("module x\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if changes.Load() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("правка go.mod не вызвала onChange")
}

// Служебные каталоги в наблюдение не берутся вовсе: .git переписывается на
// каждой команде git, и пересборка по этим событиям шла бы непрерывно.
func TestWatchGoContext_SkipsServiceDirs(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git", "objects")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var changes atomic.Int32
	done, err := WatchGoContext(ctx, dir, func() { changes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		<-done
	}()

	time.Sleep(50 * time.Millisecond)
	// Файл с расширением .go внутри пропущенного каталога — событий быть не должно
	// (в node_modules и .git такие файлы попадают как чужие исходники и мусор).
	if err := os.WriteFile(filepath.Join(gitDir, "hook.go"), []byte("package hooks"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.go"), []byte("package pkg"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if got := changes.Load(); got != 0 {
		t.Fatalf("служебный каталог вызвал пересборку %d раз", got)
	}
}
