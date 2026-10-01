package launcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// Сопутствующие приложения: лаунчер запускает только объявленное манифестом
// рядом с его собственным исполняемым файлом. Тесты держат именно эту границу —
// и то, что отсутствие companion работу не ломает.

// withLauncherDir подменяет каталог лаунчера на временный и кладёт туда манифест.
func withLauncherDir(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, companionManifestName), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	saved := launcherDir
	launcherDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { launcherDir = saved })
	return dir
}

func TestCompanionManifestRejectsPathsOutsideLauncherDir(t *testing.T) {
	bad := []struct{ exec, why string }{
		{"", "пустой exec"},
		{`C:\Windows\System32\cmd.exe`, "абсолютный путь Windows"},
		{"/usr/bin/sh", "абсолютный путь POSIX"},
		{`\\server\share\app.exe`, "сетевой путь"},
		{"C:app.exe", "путь с указанием диска относителен каталогу диска, а не лаунчера"},
		{"../outside.exe", "выход наверх"},
		{"companions/../../outside.exe", "выход наверх через подкаталог"},
	}
	for _, c := range bad {
		if err := validateCompanionExec(c.exec); err == nil {
			t.Errorf("%q (%s): ожидалась ошибка", c.exec, c.why)
		}
	}

	ok := []string{
		"app.exe",
		"companions/callista-operator/callista-operator.exe",
		`companions\callista-operator\callista-operator.exe`,
	}
	for _, e := range ok {
		if err := validateCompanionExec(e); err != nil {
			t.Errorf("%q: неожиданная ошибка: %v", e, err)
		}
	}
}

// Дистрибутив без манифеста нормален: companion просто нет.
func TestCompanionManifestAbsentIsNotAnError(t *testing.T) {
	withLauncherDir(t, "")
	specs, err := loadCompanionManifest()
	if err != nil {
		t.Fatalf("отсутствие манифеста не должно быть ошибкой: %v", err)
	}
	if len(specs) != 0 {
		t.Errorf("ожидался пустой набор, получили %v", specs)
	}
}

func TestCompanionManifestRejectsUnsafeDeclaration(t *testing.T) {
	withLauncherDir(t, "companions:\n  bad:\n    exec: \"/usr/bin/sh\"\n")
	if _, err := loadCompanionManifest(); err == nil {
		t.Fatal("манифест с абсолютным путём должен быть отвергнут целиком")
	}
}

func TestCompanionEnsureStartsOnceAndReportsState(t *testing.T) {
	withLauncherDir(t, "companions:\n  softphone:\n    exec: companions/sp/sp.exe\n    args: [\"--minimized\"]\n")
	c := newCompanionRunner()
	var started []*exec.Cmd
	c.startCmd = func(cmd *exec.Cmd) error {
		started = append(started, cmd)
		return nil
	}

	if err := c.Ensure("softphone"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(started) != 1 {
		t.Fatalf("запусков %d, ожидался 1", len(started))
	}
	// Аргументы берутся только из манифеста.
	if got := started[0].Args[1:]; len(got) != 1 || got[0] != "--minimized" {
		t.Errorf("аргументы = %v, ожидались из манифеста", got)
	}
	// Рабочий каталог — каталог приложения: Electron ищет рядом resources.
	if filepath.Base(started[0].Dir) != "sp" {
		t.Errorf("рабочий каталог = %q, ожидался каталог приложения", started[0].Dir)
	}

	// Повторное открытие рабочего места не плодит второй экземпляр.
	if err := c.Ensure("softphone"); err != nil {
		t.Fatalf("повторный Ensure: %v", err)
	}
	if len(started) != 1 {
		t.Fatalf("повторный Ensure запустил второй экземпляр: %d", len(started))
	}

	st := c.States([]string{"softphone"})
	if len(st) != 1 || !st[0].Running || !st[0].Declared || st[0].Failed {
		t.Errorf("состояние после запуска: %+v", st)
	}
}

// Неизвестное имя — не ошибка запуска, а отсутствие companion в дистрибутиве:
// лаунчер мог обновиться без него, и работа от этого встать не должна.
func TestCompanionUnknownNameIsDistinguishable(t *testing.T) {
	withLauncherDir(t, "companions:\n  softphone:\n    exec: sp.exe\n")
	c := newCompanionRunner()
	c.startCmd = func(*exec.Cmd) error { return nil }

	err := c.Ensure("scanner")
	if !errors.Is(err, ErrCompanionUnknown) {
		t.Fatalf("ожидался ErrCompanionUnknown, получили %v", err)
	}
	st := c.States([]string{"scanner"})
	if len(st) != 1 || st[0].Declared || st[0].Running {
		t.Errorf("необъявленный companion: %+v", st)
	}
}

// Неудачу запуска видно отдельно от «не запускали»: молчаливое «не работает»
// выглядит так, будто companion и не был нужен.
func TestCompanionStartFailureIsVisible(t *testing.T) {
	withLauncherDir(t, "companions:\n  softphone:\n    exec: sp.exe\n")
	c := newCompanionRunner()
	c.startCmd = func(*exec.Cmd) error { return errors.New("файл не найден") }

	if err := c.Ensure("softphone"); err == nil {
		t.Fatal("ожидалась ошибка запуска")
	}
	st := c.States([]string{"softphone"})
	if len(st) != 1 || st[0].Running || !st[0].Failed {
		t.Fatalf("состояние после неудачи: %+v", st)
	}
	if !strings.Contains(st[0].Err, "файл не найден") {
		t.Errorf("причина не сохранена: %q", st[0].Err)
	}
}

// Открытие рабочего места не должно зависеть от companion: ensureCompanions
// только логирует неудачу. Здесь важно, что nil-runner и пустой список не падают.
func TestEnsureCompanionsToleratesMissingSetup(t *testing.T) {
	h := &handler{}
	h.ensureCompanions(&Base{Name: "База", Companions: []string{"softphone"}})
	h.ensureCompanions(nil)

	h.companions = newCompanionRunner()
	h.ensureCompanions(&Base{Name: "База"})
	if got := h.companionStates(&Base{Name: "База"}); got != nil {
		t.Errorf("без объявленных companion состояний быть не должно: %v", got)
	}
}

// Успешный выход — не падение. Так выглядят два обычных случая: пользователь
// закрыл приложение и single-instance отдал управление уже работающему
// экземпляру. Второе поймано на настоящем Callista Operator: запуск второго
// дистрибутива поверх первого завершился с кодом 0 за секунды, и лаунчер
// показывал «не запустилось» про работающее приложение.
func TestCompanionSuccessfulExitIsNotFailure(t *testing.T) {
	withLauncherDir(t, "companions:\n  softphone:\n    exec: sp.exe\n")
	c := newCompanionRunner()
	c.startCmd = func(*exec.Cmd) error { return nil }
	if err := c.Ensure("softphone"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// Процесс завершился сам, без ошибки — как при передаче управления.
	c.mu.Lock()
	p := c.procs["softphone"]
	p.exited, p.exitErr = true, nil
	c.mu.Unlock()

	st := c.States([]string{"softphone"})[0]
	if st.Failed {
		t.Error("успешный выход показан как падение")
	}
	if !st.Exited {
		t.Error("успешный выход не отмечен как завершение")
	}
	if st.Err != "" {
		t.Errorf("у успешного выхода появился текст ошибки: %q", st.Err)
	}
}

// Завершение С ОШИБКОЙ остаётся падением — иначе настоящая поломка станет
// невидимой.
func TestCompanionFailedExitStaysFailure(t *testing.T) {
	withLauncherDir(t, "companions:\n  softphone:\n    exec: sp.exe\n")
	c := newCompanionRunner()
	c.startCmd = func(*exec.Cmd) error { return nil }
	if err := c.Ensure("softphone"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	c.mu.Lock()
	p := c.procs["softphone"]
	p.exited, p.exitErr = true, errors.New("exit status 1")
	c.mu.Unlock()

	st := c.States([]string{"softphone"})[0]
	if !st.Failed || st.Exited {
		t.Errorf("завершение с ошибкой классифицировано неверно: %+v", st)
	}
	if !strings.Contains(st.Err, "exit status 1") {
		t.Errorf("причина потеряна: %q", st.Err)
	}
}

// Два одновременных открытия рабочего места поднимают помощник РОВНО ОДИН раз.
//
// Проверяется через настоящие параллельные POST /bases/{id}/start: раньше
// мьютекс отпускался до запуска, а место в procs занималось после него, поэтому
// оба запроса успевали увидеть пустое место. Первый startCmd держится до входа
// второго — без этого гонка не воспроизводится стабильно.
func TestCompanionSingleStartOnConcurrentOpens(t *testing.T) {
	lab := withLauncherDir(t, "companions:\n  softphone:\n    exec: sp.exe\n")
	_ = lab

	dir := t.TempDir()
	store := &Store{path: filepath.Join(dir, "ibases.yaml")}
	// Клиентская запись: ensureBaseReady для неё не поднимает локальную базу, и
	// тест остаётся про companion, а не про запуск платформы.
	base, err := NewClientBase("Сервер", "https://srv:8443")
	if err != nil {
		t.Fatal(err)
	}
	base.Companions = []string{"softphone"}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}

	runner := newCompanionRunner()
	var mu sync.Mutex
	starts := 0
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	runner.startCmd = func(*exec.Cmd) error {
		mu.Lock()
		starts++
		n := starts
		mu.Unlock()
		entered <- struct{}{}
		if n == 1 {
			// Держим первый запуск, пока второй запрос не дойдёт до проверки.
			<-release
		}
		return nil
	}

	h := &handler{store: store, runner: NewRunner(), companions: runner}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/bases/"+base.ID+"/start", nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", base.ID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			h.start(httptest.NewRecorder(), req)
		}()
	}

	// Ждём вход первого запуска, затем даём второму запросу время дойти до
	// проверки занятости и отпускаем первый.
	<-entered
	time.Sleep(150 * time.Millisecond)
	close(release)
	wg.Wait()

	mu.Lock()
	got := starts
	mu.Unlock()
	if got != 1 {
		t.Fatalf("запусков помощника %d, ожидался 1 — два открытия подняли второй экземпляр", got)
	}
	if st := runner.States([]string{"softphone"}); len(st) != 1 || !st[0].Running {
		t.Errorf("состояние помощника после параллельных открытий: %+v", st)
	}
}

// Неудачный запуск снимает резервацию: иначе место останется занятым навсегда и
// повторная попытка станет невозможной.
func TestCompanionReservationReleasedOnStartFailure(t *testing.T) {
	withLauncherDir(t, "companions:\n  softphone:\n    exec: sp.exe\n")
	c := newCompanionRunner()
	attempts := 0
	c.startCmd = func(*exec.Cmd) error {
		attempts++
		if attempts == 1 {
			return errors.New("файл не найден")
		}
		return nil
	}

	if err := c.Ensure("softphone"); err == nil {
		t.Fatal("ожидалась ошибка первого запуска")
	}
	// Вторая попытка обязана дойти до запуска, а не упереться в резервацию.
	if err := c.Ensure("softphone"); err != nil {
		t.Fatalf("повторная попытка после неудачи: %v", err)
	}
	if attempts != 2 {
		t.Errorf("попыток запуска %d, ожидалось 2 — резервация не снята", attempts)
	}
}
