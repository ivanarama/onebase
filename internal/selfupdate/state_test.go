package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// isolatedHome уводит ~/.onebase во временный каталог: тесты не должны трогать
// реальный реестр баз и состояние обновлений пользователя.
func isolatedHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)        // Linux/macOS
	t.Setenv("USERPROFILE", dir) // Windows
	return dir
}

func TestState_RoundTrip(t *testing.T) {
	isolatedHome(t)

	want := State{
		Channel:      ChannelBuild,
		CheckedAt:    time.Now().UTC().Truncate(time.Second),
		Current:      "build-660",
		Latest:       &RelInfo{Tag: "build-672", Notes: "## Изменения\n- фикс"},
		Staged:       &StagedInfo{Tag: "build-672", Dir: "d", Files: []string{"onebase"}, Verified: true},
		RestartBases: []string{"base-1", "base-2"},
	}
	if err := SaveState(want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.Channel != want.Channel || got.Current != want.Current {
		t.Fatalf("канал/версия не сохранились: %+v", got)
	}
	if got.Latest == nil || got.Latest.Tag != "build-672" {
		t.Fatalf("latest не сохранён: %+v", got.Latest)
	}
	if !got.StagedReady() {
		t.Fatalf("staged не сохранён: %+v", got.Staged)
	}
	if len(got.RestartBases) != 2 {
		t.Fatalf("список баз не сохранён: %v", got.RestartBases)
	}
}

func TestRestartRecordsRoundTripAndRecoveryPending(t *testing.T) {
	isolatedHome(t)
	want := State{RestartRecords: []RestartRecord{{ID: "base-1", Generation: "ct1:abc"}}}
	if !want.RecoveryPending() {
		t.Fatal("generation-bound recovery record is not reported pending")
	}
	if err := SaveState(want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.RestartRecords) != 1 || got.RestartRecords[0] != want.RestartRecords[0] {
		t.Fatalf("restart records round trip: got %+v, want %+v", got.RestartRecords, want.RestartRecords)
	}

	clone := cloneState(got)
	clone.RestartRecords[0].Generation = "changed"
	if got.RestartRecords[0].Generation != "ct1:abc" {
		t.Fatal("cloneState shares RestartRecords backing array")
	}
}

func TestLegacyRestartBasesRemainReadableAndPending(t *testing.T) {
	isolatedHome(t)
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(updates, StateFileName), []byte(`{"restart_bases":["legacy"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.RestartBases) != 1 || got.RestartBases[0] != "legacy" || !got.RecoveryPending() {
		t.Fatalf("legacy recovery state not loaded safely: %+v", got)
	}
}

func TestState_AbsentIsEmpty(t *testing.T) {
	isolatedHome(t)

	st, err := LoadState()
	if err != nil {
		t.Fatalf("отсутствие файла — не ошибка: %v", err)
	}
	if st.Latest != nil || st.Staged != nil {
		t.Fatalf("ждали пустое состояние, получили %+v", st)
	}
	if st.ChannelOrDefault() != DefaultChannel {
		t.Fatalf("канал по умолчанию %q", st.ChannelOrDefault())
	}
}

func TestLoadState_DoesNotCreateLockInExistingEmptyDirectory(t *testing.T) {
	isolatedHome(t)
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(updates)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("read-only LoadState created files: %v", entries)
	}
}

// Битый файл не должен ронять лаунчер: он обязан подняться в любом случае.
func TestState_BrokenJSON(t *testing.T) {
	isolatedHome(t)
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(updates, StateFileName), []byte("{это не json"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := LoadState()
	if err == nil {
		t.Fatal("ждали ошибку разбора")
	}
	if st.Latest != nil || st.Channel != "" {
		t.Fatalf("при ошибке состояние должно быть пустым: %+v", st)
	}
}

func TestState_UpdateAvailable(t *testing.T) {
	st := State{Latest: &RelInfo{Tag: "build-672"}}
	if !st.UpdateAvailable("build-660") {
		t.Fatal("build-672 новее build-660")
	}
	if st.UpdateAvailable("build-672") {
		t.Fatal("та же версия обновлением не считается")
	}
	if st.UpdateAvailable("dev-abc1234") {
		t.Fatal("локальную сборку не обновляем")
	}
	if (State{}).UpdateAvailable("build-1") {
		t.Fatal("без сведений о релизе обновления нет")
	}
}

func TestState_NotesTrimmed(t *testing.T) {
	isolatedHome(t)

	long := strings.Repeat("я", maxNotesRunes+500)
	if err := SaveState(State{Latest: &RelInfo{Tag: "build-1", Notes: long}}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	runes := []rune(got.Latest.Notes)
	if len(runes) > maxNotesRunes+1 {
		t.Fatalf("описание не обрезано: %d рун", len(runes))
	}
	// Обрезка по рунам, а не по байтам: кириллица не должна разваливаться.
	if !strings.HasSuffix(got.Latest.Notes, "…") || strings.Contains(got.Latest.Notes, "�") {
		t.Fatalf("описание обрезано неаккуратно: %q", string(runes[len(runes)-5:]))
	}
}

func TestUpdateState_ConcurrentMutationsDoNotLoseFields(t *testing.T) {
	isolatedHome(t)

	if err := SaveState(State{AutoApply: true}); err != nil {
		t.Fatal(err)
	}
	const mutations = 32
	start := make(chan struct{})
	errs := make(chan error, mutations)
	var wg sync.WaitGroup
	for i := 0; i < mutations; i++ {
		id := fmt.Sprintf("base-%02d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := UpdateState(func(st *State) error {
				st.RestartBases = append(st.RestartBases, id)
				return nil
			})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("UpdateState: %v", err)
		}
	}

	got, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !got.AutoApply {
		t.Fatal("unrelated auto_apply field was lost")
	}
	seen := make(map[string]bool, mutations)
	for _, id := range got.RestartBases {
		seen[id] = true
	}
	if len(seen) != mutations {
		t.Fatalf("lost concurrent state mutations: got %d unique bases, want %d (%v)", len(seen), mutations, got.RestartBases)
	}
}

func TestUpdateState_ErrorLeavesFileUnchanged(t *testing.T) {
	isolatedHome(t)
	want := State{Channel: ChannelBuild, RestartBases: []string{"base-1"}}
	if err := SaveState(want); err != nil {
		t.Fatal(err)
	}

	sentinel := errors.New("stop mutation")
	returned, err := UpdateState(func(st *State) error {
		st.Channel = ChannelStable
		st.RestartBases = nil
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("UpdateState error = %v, want %v", err, sentinel)
	}
	if returned.Channel != want.Channel || len(returned.RestartBases) != 1 || returned.RestartBases[0] != "base-1" {
		t.Fatalf("failed mutation returned attempted rather than persisted state: %+v", returned)
	}
	got, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Channel != want.Channel || len(got.RestartBases) != 1 || got.RestartBases[0] != "base-1" {
		t.Fatalf("failed mutation changed state: %+v", got)
	}
}

func TestUpdateState_CrossProcessLockPreventsLostMutation(t *testing.T) {
	home := isolatedHome(t)
	if err := SaveState(State{}); err != nil {
		t.Fatal(err)
	}

	controlDir := t.TempDir()
	lockedMarker := filepath.Join(controlDir, "locked")
	releaseMarker := filepath.Join(controlDir, "release")
	cmd := exec.Command(os.Args[0], "-test.run=^TestStateLockHelperProcess$", "-test.count=1") //nolint:gosec // G204: controlled re-exec of this test binary
	cmd.Env = append(os.Environ(),
		"ONEBASE_STATE_LOCK_HELPER=1",
		"ONEBASE_STATE_LOCKED_MARKER="+lockedMarker,
		"ONEBASE_STATE_RELEASE_MARKER="+releaseMarker,
		"HOME="+home,
		"USERPROFILE="+home,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			_ = os.WriteFile(releaseMarker, []byte("release"), 0o600)
		}
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(lockedMarker); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not acquire the state lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	started := make(chan struct{})
	parentDone := make(chan error, 1)
	go func() {
		close(started)
		_, err := UpdateState(func(st *State) error {
			st.RestartBases = []string{"base-parent"}
			return nil
		})
		parentDone <- err
	}()
	<-started
	select {
	case err := <-parentDone:
		t.Fatalf("parent mutation was not blocked by the helper lock (err=%v)", err)
	case <-time.After(250 * time.Millisecond):
	}

	if err := os.WriteFile(releaseMarker, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	released = true
	if err := cmd.Wait(); err != nil {
		t.Fatalf("state-lock helper: %v", err)
	}
	if err := <-parentDone; err != nil {
		t.Fatalf("parent UpdateState: %v", err)
	}

	got, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Channel != ChannelStable || len(got.RestartBases) != 1 || got.RestartBases[0] != "base-parent" {
		t.Fatalf("cross-process mutations were not merged: %+v", got)
	}
}

func TestStateLockHelperProcess(t *testing.T) {
	if os.Getenv("ONEBASE_STATE_LOCK_HELPER") != "1" {
		return
	}
	lockedMarker := os.Getenv("ONEBASE_STATE_LOCKED_MARKER")
	releaseMarker := os.Getenv("ONEBASE_STATE_RELEASE_MARKER")
	_, err := UpdateState(func(st *State) error {
		st.Channel = ChannelStable
		if err := os.WriteFile(lockedMarker, []byte("locked"), 0o600); err != nil { //nolint:gosec // G703: parent test supplies a private t.TempDir path
			return err
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(releaseMarker); err == nil { //nolint:gosec // G703: parent test supplies a private t.TempDir path
				return nil
			} else if !os.IsNotExist(err) {
				return err
			}
			if time.Now().After(deadline) {
				return errors.New("timed out waiting for state-lock release")
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Тег приходит от GitHub — он не должен уводить запись из каталога обновлений.
func TestStageDir_TagIsSanitized(t *testing.T) {
	isolatedHome(t)

	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := StageDir("../../evil")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != updates {
		t.Fatalf("staging уехал из каталога обновлений: %s", dir)
	}
}
