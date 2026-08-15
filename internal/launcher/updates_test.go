package launcher

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/installtest"
	"github.com/ivantit66/onebase/internal/selfupdate"
	"github.com/ivantit66/onebase/internal/version"
)

// isolatedUpdatesHome уводит ~/.onebase во временный каталог: тесты не должны
// трогать ни реестр баз, ни состояние обновлений пользователя.
// isolatedUpdatesHome уводит состояние обновлений в приватный домашний каталог.
//
// Приватный, а не просто временный: каталог платформы вычисляется от HOME, и
// selfupdate законно отказывается обновлять установку из общего /tmp. С обычным
// t.TempDir() половина тестов обновления получала 403 «нет прав на запись в
// каталог платформы» вместо проверяемого исхода — и падала всегда, и локально,
// и на windows-раннере (#924).
func isolatedUpdatesHome(t *testing.T) {
	t.Helper()
	dir := installtest.PrivateHome(t)
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// isolatedUpdatableInstall — изоляция состояния ПЛЮС подмена каталога платформы
// на приватный.
//
// Каталог платформы вычисляется от os.Executable(), а не от HOME, поэтому под
// `go test` им оказывается временный каталог тест-бинаря — общий и потому
// непригодный для самообновления. Хендлер честно отвечал 403 «нет прав на
// запись в каталог платформы» вместо проверяемого исхода, и пять тестов
// обновления падали всегда: и локально, и на первом прогоне windows-раннера
// (#924). Дефект был в фикстуре, а выглядел как дефект продукта.
func isolatedUpdatableInstall(t *testing.T) string {
	t.Helper()
	isolatedUpdatesHome(t)
	dir := installtest.PrivateInstallDir(t)
	old := updateBinaryDir
	updateBinaryDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { updateBinaryDir = old })
	return dir
}

// По умолчанию платформа не подменяется молча: на канале build сборки выходят
// по нескольку раз в день, и тихая замена бинаря — не то, чего ждёт пользователь.
func TestApplyStagedOnStart_RequiresAutoApply(t *testing.T) {
	isolatedUpdatesHome(t)
	if err := selfupdate.SaveState(selfupdate.State{
		Staged: &selfupdate.StagedInfo{Tag: "build-999", Dir: t.TempDir(), Files: []string{"onebase"}, Verified: true},
	}); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if ApplyStagedOnStart(store, NewRunner()) {
		t.Fatal("без auto_apply обновление применяться не должно")
	}
	st, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !st.StagedReady() {
		t.Fatal("скачанное обновление должно остаться на месте — его применят кнопкой")
	}
}

// Скачанное совпадает с работающей версией — применять нечего, запись убираем,
// иначе кнопка «применить» осталась бы висеть навсегда.
func TestApplyStagedOnStart_ClearsStagedOfCurrentVersion(t *testing.T) {
	isolatedUpdatesHome(t)
	if err := selfupdate.SaveState(selfupdate.State{
		AutoApply: true,
		Staged:    &selfupdate.StagedInfo{Tag: version.String(), Dir: t.TempDir(), Files: []string{"onebase"}, Verified: true},
	}); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if ApplyStagedOnStart(store, NewRunner()) {
		t.Fatal("перезапуск ради уже работающей версии не нужен")
	}
	st, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Staged != nil {
		t.Fatalf("запись о скачанном обновлении должна быть убрана: %+v", st.Staged)
	}
}

func TestApplyStagedOnStartWaitsForGenerationBoundRecovery(t *testing.T) {
	isolatedUpdatesHome(t)
	if err := selfupdate.SaveState(selfupdate.State{
		AutoApply:      true,
		Staged:         &selfupdate.StagedInfo{Tag: version.String(), Dir: t.TempDir(), Files: []string{"onebase"}, Verified: true},
		RestartRecords: []selfupdate.RestartRecord{{ID: "base-1", Generation: restartGeneration("pending-token")}},
	}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if ApplyStagedOnStart(store, NewRunner()) {
		t.Fatal("auto-apply ran while generation-bound recovery was pending")
	}
	st, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Staged == nil || len(st.RestartRecords) != 1 {
		t.Fatalf("pending recovery state was altered: %+v", st)
	}
}

func TestApplyStagedOnStartApplyFailureClearsPrev(t *testing.T) {
	isolatedUpdatableInstall(t)
	t.Setenv(selfupdate.EnvUpdates, "")
	stageDir := t.TempDir()
	binaryName := selfupdate.BinaryName()
	if err := os.WriteFile(filepath.Join(stageDir, binaryName), []byte("staged"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := selfupdate.StagedInfo{
		Tag: "build-apply-failure", Dir: stageDir, Files: []string{binaryName}, Verified: true,
	}
	if err := selfupdate.SaveState(selfupdate.State{
		AutoApply: true,
		Staged:    &staged,
		Prev:      &selfupdate.RelInfo{Tag: "stale-prev", TargetDir: t.TempDir()},
	}); err != nil {
		t.Fatal(err)
	}

	oldApply := applyUpdate
	var calls atomic.Int32
	applyUpdate = func(*selfupdate.OperationLease, selfupdate.StagedInfo, string, string) error {
		calls.Add(1)
		return errors.New("intentional apply failure")
	}
	t.Cleanup(func() { applyUpdate = oldApply })

	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner()
	if ApplyStagedOnStart(store, runner) {
		t.Fatal("failed auto-apply requested a restart")
	}
	if calls.Load() != 1 {
		t.Fatalf("Apply calls = %d, want 1", calls.Load())
	}
	state, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Prev != nil {
		t.Fatalf("failed Apply left stale Prev: %+v", state.Prev)
	}
	if state.Staged == nil || state.Staged.Tag != staged.Tag {
		t.Fatalf("retryable staging was lost: %+v", state.Staged)
	}
	if err := runner.holdStarts(); err != nil {
		t.Fatalf("failed auto-apply leaked lifecycle lease: %v", err)
	}
	runner.AllowStarts()
}

func TestResumeAfterUpdateReturnsBinaryRecoveryFailure(t *testing.T) {
	isolatedUpdatesHome(t)
	targetDir := t.TempDir()
	oldBinaryDir, oldRecover := updateBinaryDir, recoverUpdateStatus
	updateBinaryDir = func() (string, error) { return targetDir, nil }
	recoverUpdateStatus = func(*selfupdate.OperationLease, string) (bool, error) {
		return false, errors.New("damaged durable journal")
	}
	t.Cleanup(func() {
		updateBinaryDir, recoverUpdateStatus = oldBinaryDir, oldRecover
	})
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := ResumeAfterUpdate(store, NewRunner()); err == nil {
		t.Fatal("startup swallowed binary recovery failure")
	}
}

func TestResumeAfterUpdateRequiresRestartAfterSettledTransaction(t *testing.T) {
	isolatedUpdatesHome(t)
	targetDir := t.TempDir()
	oldBinaryDir, oldRecover := updateBinaryDir, recoverUpdateStatus
	updateBinaryDir = func() (string, error) { return targetDir, nil }
	recoverUpdateStatus = func(*selfupdate.OperationLease, string) (bool, error) { return true, nil }
	t.Cleanup(func() {
		updateBinaryDir, recoverUpdateStatus = oldBinaryDir, oldRecover
	})
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := ResumeAfterUpdate(store, NewRunner()); !errors.Is(err, ErrBinaryRecoveryRestartRequired) {
		t.Fatalf("ResumeAfterUpdate error = %v, want restart required", err)
	}
}

// nonSelfUpdatableDir строит каталог, который НАСТОЯЩАЯ проверка selfupdate
// отвергает как непригодный для самообновления, — то есть общую установку:
// C:\onebase, Program Files, сетевую шару.
//
// t.TempDir() для этого не годится, и в этом суть пропущенного дефекта: на
// Windows приватность определяется реальным профилем (FOLDERID_Profile), а не
// переменной USERPROFILE, которую подменяет isolatedUpdatesHome. Временный
// каталог лежит внутри профиля, поэтому все прежние тесты гоняли ровно тот
// случай, который работал.
func nonSelfUpdatableDir(t *testing.T) string {
	t.Helper()
	var dir string
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemDrive")
		if root == "" {
			root = "C:"
		}
		created, err := os.MkdirTemp(root+string(os.PathSeparator), "onebase-shared-")
		if err != nil {
			t.Skipf("не удалось создать каталог вне профиля пользователя: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(created) }) //nolint:gosec // G703: created is the exact MkdirTemp result beneath the selected volume root
		dir = created
	} else {
		dir = t.TempDir()
		if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // G302: intentionally model a shared installation rejected by self-update
			t.Fatal(err)
		}
	}
	// Тест обязан упасть или пропуститься, но не «пройти» на каталоге, который
	// на самом деле пригоден для самообновления: тогда он не проверял бы ничего.
	if selfupdate.CanSafelyUpdateBinaryDir(dir) {
		t.Skipf("каталог %s пригоден для самообновления — общая установка не воспроизведена", dir)
	}
	return dir
}

// Установка, не участвующая в самообновлении, обязана запускаться. До правки
// ReserveTarget внутри recoverUpdateStatus возвращал оттуда ошибку «shared
// installations cannot be self-updated safely», ResumeAfterUpdate считал её
// фатальной — и лаунчер сборок 783–793 не стартовал ни из C:\onebase (куда
// распаковать велит README), ни с любого другого пути вне %USERPROFILE%.
func TestResumeAfterUpdateStartsWhenInstallationCannotSelfUpdate(t *testing.T) {
	isolatedUpdatesHome(t)
	targetDir := nonSelfUpdatableDir(t)
	oldBinaryDir := updateBinaryDir
	updateBinaryDir = func() (string, error) { return targetDir, nil }
	t.Cleanup(func() { updateBinaryDir = oldBinaryDir })
	// recoverUpdateStatus намеренно НЕ подменяется: проверяется тот же путь
	// восстановления, каким идёт настоящий запуск у пользователя.
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := ResumeAfterUpdate(store, NewRunner()); err != nil {
		t.Fatalf("лаунчер не стартовал на установке без самообновления: %v", err)
	}
	// Применять staged-обновление в такой установке тоже нечего, но это не
	// авария: гейт просто закрыт.
	if ApplyStagedOnStart(store, NewRunner()) {
		t.Fatal("ApplyStagedOnStart попытался заменить бинарь в установке без самообновления")
	}
}

// Непригодность каталога к самообновлению не доказывает, что
// транзакции нет. Если marker уже опубликован, замена бинарей могла
// начаться. При временной ошибке ACL/диска лаунчер обязан остаться
// fail-closed, а не запускать базы на возможно смешанном наборе бинарей.
func TestResumeAfterUpdateFailsClosedForMarkerInNonSelfUpdatableInstallation(t *testing.T) {
	isolatedUpdatesHome(t)
	targetDir := nonSelfUpdatableDir(t)
	marker := filepath.Join(targetDir, ".onebase-update.pending.json")
	if err := os.WriteFile(marker, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldBinaryDir := updateBinaryDir
	updateBinaryDir = func() (string, error) { return targetDir, nil }
	t.Cleanup(func() { updateBinaryDir = oldBinaryDir })
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := ResumeAfterUpdate(store, NewRunner()); err == nil {
		t.Fatal("лаунчер проигнорировал marker незавершённого обновления")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker незавершённого обновления был изменён: %v", err)
	}
}

func TestApplyStagedOnStartKeepsGateClosedForRecoveryPendingError(t *testing.T) {
	isolatedUpdatesHome(t)
	// Каталог платформы обязан быть приватным: обычный t.TempDir() лежит в общем
	// /tmp, selfupdate такую установку законно не обновляет, и тест проверял бы
	// не тот исход (#924).
	targetDir := installtest.PrivateInstallDir(t)
	stageDir := t.TempDir()
	files := selfupdate.PackageBinaries()
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(targetDir, name), []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stageDir, name), []byte("new"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	staged := selfupdate.StagedInfo{Tag: "build-pending-recovery", Dir: stageDir, Files: files, Verified: true}
	if err := selfupdate.SaveState(selfupdate.State{AutoApply: true, Staged: &staged}); err != nil {
		t.Fatal(err)
	}
	oldBinaryDir, oldRecover, oldApply := updateBinaryDir, recoverUpdate, applyUpdate
	updateBinaryDir = func() (string, error) { return targetDir, nil }
	recoverUpdate = func(*selfupdate.OperationLease, string) error { return nil }
	applyUpdate = func(*selfupdate.OperationLease, selfupdate.StagedInfo, string, string) error {
		return selfupdate.NewRecoveryPendingError(errors.New("recovery media unavailable"))
	}
	t.Cleanup(func() {
		updateBinaryDir, recoverUpdate, applyUpdate = oldBinaryDir, oldRecover, oldApply
	})
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner()
	if ApplyStagedOnStart(store, runner) {
		t.Fatal("recovery-pending apply requested restart")
	}
	if err := runner.holdStarts(); err == nil {
		runner.AllowStarts()
		t.Fatal("recovery-pending startup apply reopened lifecycle gate")
	}
	runner.AllowStarts()
}

// База из списка восстановления исчезла из реестра — список всё равно должен
// очиститься, иначе каждый следующий старт будет дёргать её заново.
func TestResumeAfterUpdate_ClearsListForMissingBase(t *testing.T) {
	isolatedUpdatesHome(t)
	if err := selfupdate.SaveState(selfupdate.State{RestartBases: []string{"нет-такой-базы"}}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}

	if err := ResumeAfterUpdate(store, NewRunner()); err != nil {
		t.Fatal(err)
	}

	st, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RestartBases) != 0 {
		t.Fatalf("список баз для восстановления не очищен: %v", st.RestartBases)
	}
}

func TestResumeAfterUpdateLegacyIDNeverStartsReusedBase(t *testing.T) {
	isolatedUpdatesHome(t)
	const id = "reused-id"
	if err := selfupdate.SaveState(selfupdate.State{RestartBases: []string{id}}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	base := &Base{ID: id, Name: "replacement", ControlToken: "new-generation", DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "new.db"), Port: 0}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}

	oldExePath := exePath
	var starts atomic.Int32
	exePath = func() (string, error) {
		starts.Add(1)
		return "", errors.New("must not start")
	}
	t.Cleanup(func() { exePath = oldExePath })

	if err := ResumeAfterUpdate(store, NewRunner()); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 0 {
		t.Fatalf("legacy recovery started reused ID %d times", starts.Load())
	}
	st, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.RecoveryPending() {
		t.Fatalf("legacy recovery record was not consumed fail-closed: %+v", st)
	}
}

func TestResumeAfterUpdateDoesNotStartDifferentGeneration(t *testing.T) {
	isolatedUpdatesHome(t)
	const id = "same-id"
	record := selfupdate.RestartRecord{ID: id, Generation: restartGeneration("old-token")}
	if err := selfupdate.SaveState(selfupdate.State{RestartRecords: []selfupdate.RestartRecord{record}}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	base := &Base{ID: id, Name: "replacement", ControlToken: "new-token", DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "new.db"), Port: 0}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}

	oldExePath := exePath
	var starts atomic.Int32
	exePath = func() (string, error) {
		starts.Add(1)
		return "", errors.New("must not start")
	}
	t.Cleanup(func() { exePath = oldExePath })

	if err := ResumeAfterUpdate(store, NewRunner()); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 0 {
		t.Fatalf("recovery started different generation %d times", starts.Load())
	}
	st, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.RecoveryPending() {
		t.Fatalf("stale generation was not consumed: %+v", st)
	}
}

func TestResumeAfterUpdateDoesNotInitializeReplacementWithoutToken(t *testing.T) {
	isolatedUpdatesHome(t)
	const id = "same-id-no-token"
	record := selfupdate.RestartRecord{ID: id, Generation: restartGeneration("old-token")}
	if err := selfupdate.SaveState(selfupdate.State{RestartRecords: []selfupdate.RestartRecord{record}}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(&Base{ID: id, Name: "replacement", DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "new.db")}); err != nil {
		t.Fatal(err)
	}

	if err := ResumeAfterUpdate(store, NewRunner()); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ControlToken != "" {
		t.Fatal("recovery initialized the token of a replacement record")
	}
}

func TestResumeAfterUpdateAttemptsSameGenerationAfterMutableEdit(t *testing.T) {
	isolatedUpdatesHome(t)
	const (
		id    = "same-generation"
		token = "persistent-token"
	)
	record := selfupdate.RestartRecord{ID: id, Generation: restartGeneration(token)}
	if err := selfupdate.SaveState(selfupdate.State{RestartRecords: []selfupdate.RestartRecord{record}}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	// Mutable fields may legitimately change while recovery is pending. The
	// persistent token, rather than name/port/path, defines the generation.
	base := &Base{ID: id, Name: "renamed", ControlToken: token, DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "moved.db")}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}

	oldExePath := exePath
	var starts atomic.Int32
	exePath = func() (string, error) {
		starts.Add(1)
		return "", errors.New("intentional start failure")
	}
	t.Cleanup(func() { exePath = oldExePath })

	if err := ResumeAfterUpdate(store, NewRunner()); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 1 {
		t.Fatalf("same generation start attempts = %d, want 1", starts.Load())
	}
	st, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.RestartRecords, []selfupdate.RestartRecord{record}) {
		t.Fatalf("failed same-generation start was not retained: %+v", st.RestartRecords)
	}
}

func TestResumeAfterUpdateHoldsStoreLockThroughGenerationCheckAndStart(t *testing.T) {
	isolatedUpdatesHome(t)
	const (
		id    = "locked-generation"
		token = "persistent-token"
	)
	record := selfupdate.RestartRecord{ID: id, Generation: restartGeneration(token)}
	if err := selfupdate.SaveState(selfupdate.State{RestartRecords: []selfupdate.RestartRecord{record}}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(&Base{
		ID: id, Name: "locked", ControlToken: token, DBType: "sqlite",
		DBPath: filepath.Join(t.TempDir(), "locked.db"),
	}); err != nil {
		t.Fatal(err)
	}

	oldExePath := exePath
	var starts atomic.Int32
	var storeWasUnlocked atomic.Bool
	var lifecycleWasUnlocked atomic.Bool
	runner := NewRunner()
	exePath = func() (string, error) {
		starts.Add(1)
		// A concurrent lifecycle mutation and Store replacement must both be
		// excluded from the generation check through process launch.
		if runner.lifecycleMu.TryLock() {
			lifecycleWasUnlocked.Store(true)
			runner.lifecycleMu.Unlock()
		}
		if storeProcessMu.TryLock() {
			storeWasUnlocked.Store(true)
			storeProcessMu.Unlock()
		}
		return "", errors.New("intentional start failure")
	}
	t.Cleanup(func() { exePath = oldExePath })

	if err := ResumeAfterUpdate(store, runner); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 1 {
		t.Fatalf("start attempts = %d, want 1", starts.Load())
	}
	if storeWasUnlocked.Load() {
		t.Fatal("Store mutation lock was available between generation check and Start")
	}
	if lifecycleWasUnlocked.Load() {
		t.Fatal("lifecycle lease was available between generation check and Start")
	}
	if !runner.lifecycleMu.TryLock() {
		t.Fatal("recovery leaked lifecycle lease")
	}
	runner.lifecycleMu.Unlock()
}

func TestRecordRecoveryResultPreservesIDsFromAnotherOperation(t *testing.T) {
	isolatedUpdatesHome(t)
	attempted := selfupdate.RestartRecord{ID: "attempted", Generation: restartGeneration("generation-1")}
	concurrent := selfupdate.RestartRecord{ID: "attempted", Generation: restartGeneration("generation-2")}
	other := selfupdate.RestartRecord{ID: "other", Generation: restartGeneration("generation-3")}
	if err := selfupdate.SaveState(selfupdate.State{
		RestartRecords: []selfupdate.RestartRecord{attempted, concurrent, other},
		Latest:         &selfupdate.RelInfo{Tag: "build-900"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := recordRecoveryResult([]selfupdate.RestartRecord{attempted}, nil, nil); err != nil {
		t.Fatal(err)
	}
	st, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.RestartRecords, []selfupdate.RestartRecord{concurrent, other}) {
		t.Fatalf("чужая recovery-запись потеряна: %v", st.RestartRecords)
	}
	if st.Latest == nil || st.Latest.Tag != "build-900" {
		t.Fatalf("несвязанное поле Latest потеряно: %+v", st.Latest)
	}
}

func TestStopAllForUpdateGuardFailsBeforeStopping(t *testing.T) {
	isolatedUpdatesHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner()
	h := &handler{store: store, runner: runner}
	st := selfupdate.State{}
	wantErr := errors.New("state changed")

	err = h.stopAllForUpdate(&st, func(*selfupdate.State) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("ошибка guard потеряна: %v", err)
	}
	if err := runner.holdStarts(); err != nil {
		t.Fatalf("lifecycle gate остался закрыт: %v", err)
	}
	runner.AllowStarts()
}

func TestUpdatesChannel_SwitchesAndDropsStaged(t *testing.T) {
	isolatedUpdatesHome(t)
	if err := selfupdate.SaveState(selfupdate.State{
		Channel: selfupdate.ChannelBuild,
		Latest:  &selfupdate.RelInfo{Tag: "build-672"},
		Staged:  &selfupdate.StagedInfo{Tag: "build-672", Dir: t.TempDir(), Verified: true},
	}); err != nil {
		t.Fatal(err)
	}

	h := &handler{}
	w := httptest.NewRecorder()
	h.updatesChannel(w, httptest.NewRequest("POST", "/updates/channel?value=stable", nil))
	if w.Code != 200 {
		t.Fatalf("код %d, тело %s", w.Code, w.Body.String())
	}

	st, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Channel != selfupdate.ChannelStable {
		t.Fatalf("канал %q, ждали stable", st.Channel)
	}
	// Скачанное принадлежало прежнему каналу — предлагать его к установке
	// после переключения нельзя.
	if st.Staged != nil || st.Latest != nil {
		t.Fatalf("сведения прежнего канала не сброшены: staged=%+v latest=%+v", st.Staged, st.Latest)
	}
}

func TestUpdatesChannel_RejectsUnknown(t *testing.T) {
	isolatedUpdatesHome(t)
	h := &handler{}
	w := httptest.NewRecorder()
	h.updatesChannel(w, httptest.NewRequest("POST", "/updates/channel?value=nightly", nil))
	if w.Code != 400 {
		t.Fatalf("код %d, ждали 400", w.Code)
	}
}

// Применять нечего — хендлер обязан сказать это, а не остановить базы «на всякий
// случай».
func TestUpdatesApply_WithoutStagedIsConflict(t *testing.T) {
	isolatedUpdatableInstall(t)
	h := &handler{}
	w := httptest.NewRecorder()
	h.updatesApply(w, httptest.NewRequest("POST", "/updates/apply", nil))
	if w.Code != 409 {
		t.Fatalf("код %d, ждали 409 (нечего применять): %s", w.Code, w.Body.String())
	}
}

func TestUpdatesApplyFailureClearsPrev(t *testing.T) {
	isolatedUpdatableInstall(t)
	t.Setenv(selfupdate.EnvUpdates, "")
	staged := selfupdate.StagedInfo{Tag: "build-apply-failure", Dir: t.TempDir(), Verified: true}
	if err := selfupdate.SaveState(selfupdate.State{
		Staged: &staged,
		Prev:   &selfupdate.RelInfo{Tag: "stale-prev", TargetDir: t.TempDir()},
	}); err != nil {
		t.Fatal(err)
	}

	oldApply := applyUpdate
	var calls atomic.Int32
	applyUpdate = func(*selfupdate.OperationLease, selfupdate.StagedInfo, string, string) error {
		calls.Add(1)
		return errors.New("intentional apply failure")
	}
	t.Cleanup(func() { applyUpdate = oldApply })

	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner()
	h := &handler{store: store, runner: runner}
	w := httptest.NewRecorder()
	h.updatesApply(w, httptest.NewRequest(http.MethodPost, "/updates/apply", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("Apply calls = %d, want 1", calls.Load())
	}
	state, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Prev != nil {
		t.Fatalf("failed Apply left stale Prev: %+v", state.Prev)
	}
	if state.Staged == nil || state.Staged.Tag != staged.Tag {
		t.Fatalf("retryable staging was lost: %+v", state.Staged)
	}
	if err := runner.holdStarts(); err != nil {
		t.Fatalf("failed manual Apply leaked lifecycle lease: %v", err)
	}
	runner.AllowStarts()
}

func TestUpdatesApplyKeepsLifecycleGateClosedWhileRecoveryIsPending(t *testing.T) {
	isolatedUpdatableInstall(t)
	t.Setenv(selfupdate.EnvUpdates, "")
	staged := selfupdate.StagedInfo{Tag: "build-recovery-pending", Dir: t.TempDir(), Verified: true}
	if err := selfupdate.SaveState(selfupdate.State{Staged: &staged}); err != nil {
		t.Fatal(err)
	}

	oldApply := applyUpdate
	applyUpdate = func(*selfupdate.OperationLease, selfupdate.StagedInfo, string, string) error {
		return selfupdate.NewRecoveryPendingError(errors.New("recovery storage unavailable"))
	}
	t.Cleanup(func() { applyUpdate = oldApply })

	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner()
	h := &handler{store: store, runner: runner}
	w := httptest.NewRecorder()
	h.updatesApply(w, httptest.NewRequest(http.MethodPost, "/updates/apply", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	done := make(chan error, 1)
	go func() { done <- runner.holdStarts() }()
	select {
	case err := <-done:
		if err == nil {
			runner.AllowStarts()
			t.Fatal("recovery-pending Apply reopened the lifecycle gate")
		}
	case <-time.After(50 * time.Millisecond):
		// Blocking is the expected safety behavior.
	}
}

func TestStopAllForUpdatePersistsAndStopsAdoptedBase(t *testing.T) {
	isolatedUpdatesHome(t)
	const token = "update-control-secret"
	processExited := make(chan struct{})
	useExitWaiter(t, processExited)

	var ts *httptest.Server
	var stopOnce sync.Once
	ts = httptest.NewServer(authenticatedControlHandler(t, token, "base-control", func() {
		stopOnce.Do(func() {
			go func() {
				ts.Close()
				close(processExited)
			}()
		})
	}))
	t.Cleanup(ts.Close)
	base := controlTestBase(t, ts, token)
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner()
	h := &handler{store: store, runner: runner}
	st := selfupdate.State{}
	if err := h.stopAllForUpdate(&st, nil); err != nil {
		t.Fatalf("stopAllForUpdate: %v", err)
	}
	defer runner.AllowStarts()
	wantRecord := selfupdate.RestartRecord{ID: base.ID, Generation: restartGeneration(base.ControlToken)}
	if !slices.Equal(st.RestartRecords, []selfupdate.RestartRecord{wantRecord}) || len(st.RestartBases) != 0 {
		t.Fatalf("adopted base missing from generation-bound recovery list: %+v", st)
	}
	saved, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved.RestartRecords, []selfupdate.RestartRecord{wantRecord}) || len(saved.RestartBases) != 0 {
		t.Fatalf("persisted recovery list: %+v", saved)
	}
	if !waitPortFree(base.Port, 2*time.Second) {
		t.Fatal("adopted base was not stopped")
	}
}

func TestStopAllForUpdateRejectsIdentityLostBetweenPreflights(t *testing.T) {
	isolatedUpdatesHome(t)
	const token = "update-control-secret"
	var identityCalls atomic.Int32
	var stopCalled atomic.Bool
	control := authenticatedControlHandler(t, token, "base-control", func() {
		stopCalled.Store(true)
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/debug/process/identity" && identityCalls.Add(1) == 2 {
			http.Error(w, "identity temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		control.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)

	adopted := controlTestBase(t, ts, token)
	tracked := &Base{
		ID: "tracked", Name: "Tracked", Port: waitReadyFreePort(t),
		ControlToken: "tracked-generation",
	}
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []*Base{tracked, adopted} {
		if err := store.Add(base); err != nil {
			t.Fatal(err)
		}
	}
	runner := NewRunner()
	runner.procs[tracked.ID] = &managedProc{
		port: tracked.Port, controlToken: tracked.ControlToken, done: make(chan struct{}),
	}
	h := &handler{store: store, runner: runner}
	var releasedTarget atomic.Bool
	st := selfupdate.State{}
	err = h.stopAllForUpdate(&st, nil, func() error {
		releasedTarget.Store(true)
		return nil
	})
	if err == nil {
		t.Fatal("update continued after the second preflight lost process identity")
	}
	if !releasedTarget.Load() {
		t.Fatal("update target reservation was not released before recovery")
	}
	if stopCalled.Load() {
		t.Fatal("strict preflight sent a stop request after identity became unverified")
	}
	if !runner.IsRunning(tracked.ID) {
		t.Fatal("strict preflight partially stopped a tracked base before rejecting the update")
	}
	if portFree(adopted.Port) {
		t.Fatal("adopted process was stopped after its identity became unverified")
	}
	if got := identityCalls.Load(); got < 3 {
		t.Fatalf("identity probes = %d, want outer + strict + recovery probes", got)
	}
	if st.RecoveryPending() {
		t.Fatalf("already-running bases were left in recovery state: %+v", st)
	}
	if err := runner.holdStarts(); err != nil {
		t.Fatalf("strict preflight leaked lifecycle gate: %v", err)
	}
	runner.AllowStarts()
}

func TestStopAllForUpdateDoesNotStopWhenRecoveryStateCannotBeSaved(t *testing.T) {
	badHome := filepath.Join(t.TempDir(), "home-is-a-file")
	if err := os.WriteFile(badHome, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", badHome)
	t.Setenv("USERPROFILE", badHome)

	const token = "update-control-secret"
	var stopCalled atomic.Bool
	ts := httptest.NewServer(authenticatedControlHandler(t, token, "base-control", func() {
		stopCalled.Store(true)
	}))
	t.Cleanup(ts.Close)
	base := controlTestBase(t, ts, token)
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner()
	h := &handler{store: store, runner: runner}
	if err := h.stopAllForUpdate(&selfupdate.State{}, nil); err == nil {
		t.Fatal("unwritable recovery state must abort update")
	}
	if stopCalled.Load() || portFree(base.Port) {
		t.Fatal("base was stopped despite recovery-state failure")
	}
	if err := runner.holdStarts(); err != nil {
		t.Fatalf("lifecycle gate leaked after aborted update: %v", err)
	}
	runner.AllowStarts()
}

func TestConcurrentUpdateOperationIsRejected(t *testing.T) {
	h := &handler{}
	h.updateMu.Lock()
	defer h.updateMu.Unlock()
	rec := httptest.NewRecorder()
	h.updatesCheck(rec, httptest.NewRequest(http.MethodPost, "/updates/check", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("concurrent update code %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestQuiescingLauncherRejectsAllRequestsAndSecondUpdate(t *testing.T) {
	h := &handler{}
	h.updateQuiescing.Store(true)

	nextCalled := atomic.Bool{}
	handler := h.rejectWhileUpdateQuiescing(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled.Store(true)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/bases/anything/start", nil))
	if rec.Code != http.StatusServiceUnavailable || nextCalled.Load() {
		t.Fatalf("quiescing middleware code=%d next=%v body=%s", rec.Code, nextCalled.Load(), rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.updatesApply(rec, httptest.NewRequest(http.MethodPost, "/updates/apply", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("second update during handoff code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRestartAfterResponseFailureStillQuiescesAndQuits(t *testing.T) {
	oldRestart := restartSelf
	restartSelf = func() error { return errors.New("injected restart failure") }
	t.Cleanup(func() { restartSelf = oldRestart })

	quit := make(chan struct{}, 1)
	h := &handler{runner: NewRunner(), quitFn: func() { quit <- struct{}{} }}
	h.restartAfterResponse()
	if !h.updateQuiescing.Load() {
		t.Fatal("restart handoff did not synchronously quiesce old launcher")
	}
	select {
	case <-quit:
	case <-time.After(2 * time.Second):
		t.Fatal("restart failure left old launcher serving indefinitely")
	}
	if !h.updateQuiescing.Load() {
		t.Fatal("restart failure reopened old launcher")
	}
}
