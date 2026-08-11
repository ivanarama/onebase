package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// installation готовит «установку» из двух бинарей и staging с новыми версиями.
// Имена намеренно свои, а не из PackageBinaries: Apply работает по списку
// staged.Files, и тест не должен зависеть от платформы.
func installation(t *testing.T) (targetDir string, staged StagedInfo) {
	t.Helper()
	targetDir = filepath.Join(t.TempDir(), "bin")
	stageDir := filepath.Join(t.TempDir(), "stage")
	for _, d := range []string{targetDir, stageDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{"onebase-a", "onebase-b"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(targetDir, n), []byte("СТАРЫЙ-"+n), 0o755); err != nil { //nolint:gosec // G306: это исполняемый файл
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stageDir, n), []byte("НОВЫЙ-"+n), 0o755); err != nil { //nolint:gosec // G306: это исполняемый файл
			t.Fatal(err)
		}
	}
	return targetDir, StagedInfo{Tag: "build-672", Dir: stageDir, Files: names, Verified: true}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: путь из теста
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestApply_ReplacesAllBinariesAndKeepsPrev(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := installation(t)

	if err := Apply(staged, targetDir); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, n := range staged.Files {
		if got := read(t, filepath.Join(targetDir, n)); got != "НОВЫЙ-"+n {
			t.Fatalf("%s не заменён: %q", n, got)
		}
	}

	prev, err := PrevDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range staged.Files {
		if got := read(t, filepath.Join(prev, n)); got != "СТАРЫЙ-"+n {
			t.Fatalf("%s не сохранён для отката: %q", n, got)
		}
	}
	// После успешного применения staging не нужен — он весит десятки мегабайт.
	if _, err := os.Stat(staged.Dir); !os.IsNotExist(err) {
		t.Fatalf("каталог обновления не очищен (err=%v)", err)
	}
	// Резервных копий рядом с бинарями остаться не должно.
	if _, err := os.Stat(filepath.Join(targetDir, "onebase-a.old")); !os.IsNotExist(err) {
		t.Fatal(".old остался рядом с бинарём")
	}
}

func TestRollbackPrev_RestoresPreviousBinaries(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := installation(t)
	if err := Apply(staged, targetDir); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if err := RollbackPrev(targetDir); err != nil {
		t.Fatalf("RollbackPrev: %v", err)
	}
	for _, n := range staged.Files {
		if got := read(t, filepath.Join(targetDir, n)); got != "СТАРЫЙ-"+n {
			t.Fatalf("%s не откачен: %q", n, got)
		}
	}

	// Откат одноразовый: второй вызов обязан честно сказать, что возвращаться
	// уже некуда, а не отчитаться успехом о той же самой версии.
	if err := RollbackPrev(targetDir); err == nil {
		t.Fatal("повторный откат должен отказывать — предыдущей версии больше нет")
	}
}

func TestRollbackPrev_WithoutPrevFails(t *testing.T) {
	isolatedHome(t)
	targetDir, _ := installation(t)

	if err := RollbackPrev(targetDir); err == nil {
		t.Fatal("откатываться некуда — ждали ошибку")
	}
}

// Второе обновление подряд: рядом с бинарём мог остаться .old от прошлой
// попытки, а Windows не переименует поверх существующего файла.
func TestApply_TwiceInARow(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := installation(t)
	if err := Apply(staged, targetDir); err != nil {
		t.Fatalf("первое применение: %v", err)
	}
	// Имитируем остаток прошлой попытки, который не успели убрать.
	if err := os.WriteFile(filepath.Join(targetDir, "onebase-a.old"), []byte("остаток"), 0o755); err != nil { //nolint:gosec // G306: это исполняемый файл
		t.Fatal(err)
	}

	_, next := installation(t)
	// Второе обновление кладём в тот же каталог установки.
	for _, n := range next.Files {
		if err := os.WriteFile(filepath.Join(next.Dir, n), []byte("НОВЕЙШИЙ-"+n), 0o755); err != nil { //nolint:gosec // G306: это исполняемый файл
			t.Fatal(err)
		}
	}
	if err := Apply(next, targetDir); err != nil {
		t.Fatalf("второе применение: %v", err)
	}
	if got := read(t, filepath.Join(targetDir, "onebase-a")); got != "НОВЕЙШИЙ-onebase-a" {
		t.Fatalf("бинарь не заменён вторым обновлением: %q", got)
	}
}

func TestApply_UnverifiedRefused(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := installation(t)
	staged.Verified = false

	if err := Apply(staged, targetDir); err == nil {
		t.Fatal("непроверенное обновление применять нельзя")
	}
	if got := read(t, filepath.Join(targetDir, "onebase-a")); got != "СТАРЫЙ-onebase-a" {
		t.Fatalf("бинарь всё-таки подменили: %q", got)
	}
}

// Установка без GUI-бинаря: обновление не должно добавлять файлы, которых у
// пользователя не было.
func TestApply_SkipsBinariesAbsentInInstallation(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := installation(t)
	if err := os.Remove(filepath.Join(targetDir, "onebase-b")); err != nil {
		t.Fatal(err)
	}

	if err := Apply(staged, targetDir); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "onebase-b")); !os.IsNotExist(err) {
		t.Fatal("обновление добавило бинарь, которого не было в установке")
	}
}

// Платформа из двух разных версий хуже, чем неудавшееся обновление: если второй
// бинарь заменить не удалось, первый обязан вернуться.
func TestApply_RollsBackOnPartialFailure(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := installation(t)
	// Файл заявлен в обновлении, но в staging его нет — подмена сорвётся на
	// втором шаге, когда первый бинарь уже заменён.
	if err := os.Remove(filepath.Join(staged.Dir, "onebase-b")); err != nil {
		t.Fatal(err)
	}

	if err := Apply(staged, targetDir); err == nil {
		t.Fatal("ждали ошибку применения")
	}
	for _, n := range staged.Files {
		if got := read(t, filepath.Join(targetDir, n)); got != "СТАРЫЙ-"+n {
			t.Fatalf("%s остался от сорвавшегося обновления: %q", n, got)
		}
	}
}

func TestApply_NothingToReplace(t *testing.T) {
	isolatedHome(t)
	_, staged := installation(t)
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Apply(staged, empty); err == nil {
		t.Fatal("в каталоге нет бинарей платформы — ждали ошибку")
	}
}

// Fetch обязан убедиться, что скачанный бинарь — той версии, за которую себя
// выдаёт: иначе обновление применит непонятно что.
func TestFetch_RefusesVersionMismatch(t *testing.T) {
	isolatedHome(t)

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "onebase.zip")
	entries := map[string]string{}
	for _, name := range PackageBinaries() {
		entries["dist/"+name] = "БИНАРЬ-" + name
	}
	writeZip(t, archivePath, entries)
	body, err := os.ReadFile(archivePath) //nolint:gosec // G304: путь из теста
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)

	mux := http.NewServeMux()
	mux.HandleFunc("/a.zip", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	mux.HandleFunc("/a.zip.sha256", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  a.zip\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	orig := binaryVersion
	t.Cleanup(func() { binaryVersion = orig })
	binaryVersion = func(string) (string, error) { return "build-100", nil }

	rel := Release{Tag: "build-672", AssetName: "a.zip", AssetURL: srv.URL + "/a.zip", SHAURL: srv.URL + "/a.zip.sha256"}
	if _, err := Fetch(context.Background(), rel); err == nil {
		t.Fatal("версия бинаря не совпала с тегом релиза — ждали отказ")
	}

	// Успешный путь: версия сошлась — обновление помечено готовым.
	binaryVersion = func(string) (string, error) { return "build-672", nil }
	staged, err := Fetch(context.Background(), rel)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !staged.Verified || staged.Tag != "build-672" {
		t.Fatalf("staged не готов: %+v", staged)
	}
	st, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !st.StagedReady() {
		t.Fatalf("состояние не запомнило скачанное обновление: %+v", st.Staged)
	}
}

func TestCheck_PreservesStateChangedWhileNetworkRequestIsInFlight(t *testing.T) {
	isolatedHome(t)
	if err := SaveState(State{Channel: ChannelBuild, AutoApply: true}); err != nil {
		t.Fatal(err)
	}

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	lookup := func(context.Context, string, Channel) (Release, error) {
		close(requestStarted)
		<-releaseRequest
		return Release{Tag: "build-999", Notes: "notes", HTMLURL: "https://example.invalid/build-999"}, nil
	}
	type result struct {
		state State
		err   error
	}
	done := make(chan result, 1)
	go func() {
		state, err := check(context.Background(), Options{}, lookup)
		done <- result{state: state, err: err}
	}()
	<-requestStarted
	if _, err := UpdateState(func(st *State) error {
		st.RestartBases = []string{"base-started-during-check"}
		return nil
	}); err != nil {
		close(releaseRequest)
		t.Fatal(err)
	}
	close(releaseRequest)

	got := <-done
	if got.err != nil {
		t.Fatalf("Check: %v", got.err)
	}
	if got.state.Latest == nil || got.state.Latest.Tag != "build-999" {
		t.Fatalf("latest release was not saved: %+v", got.state.Latest)
	}
	if !got.state.AutoApply || len(got.state.RestartBases) != 1 || got.state.RestartBases[0] != "base-started-during-check" {
		t.Fatalf("Check erased fields changed during its network request: %+v", got.state)
	}

	persisted, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.RestartBases) != 1 || persisted.RestartBases[0] != "base-started-during-check" {
		t.Fatalf("restart_bases was erased on disk: %v", persisted.RestartBases)
	}
}

func TestCheck_SlowerOlderRequestCannotOverwriteNewerCheck(t *testing.T) {
	isolatedHome(t)

	type result struct {
		state State
		err   error
	}
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan result, 1)
	go func() {
		state, err := check(context.Background(), Options{Channel: ChannelBuild}, func(context.Context, string, Channel) (Release, error) {
			close(firstStarted)
			<-releaseFirst
			return Release{Tag: "build-100"}, nil
		})
		firstDone <- result{state: state, err: err}
	}()
	<-firstStarted

	secondStarted := make(chan struct{})
	releaseSecond := make(chan struct{})
	secondDone := make(chan result, 1)
	go func() {
		state, err := check(context.Background(), Options{Channel: ChannelStable}, func(context.Context, string, Channel) (Release, error) {
			close(secondStarted)
			<-releaseSecond
			return Release{Tag: "v2.0.0"}, nil
		})
		secondDone <- result{state: state, err: err}
	}()
	<-secondStarted

	close(releaseSecond)
	second := <-secondDone
	if second.err != nil {
		close(releaseFirst)
		t.Fatalf("newer Check: %v", second.err)
	}
	close(releaseFirst)
	first := <-firstDone
	if first.err != nil {
		t.Fatalf("older Check: %v", first.err)
	}

	got, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Channel != ChannelStable || got.Latest == nil || got.Latest.Tag != "v2.0.0" {
		t.Fatalf("older completion overwrote newer check: %+v", got)
	}
	if first.state.Channel != ChannelStable || first.state.Latest == nil || first.state.Latest.Tag != "v2.0.0" {
		t.Fatalf("stale Check returned its obsolete result: %+v", first.state)
	}
}

func TestFetch_ConcurrentSameTagUsesSinglePublishedStage(t *testing.T) {
	isolatedHome(t)

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "onebase.zip")
	entries := map[string]string{}
	for _, name := range PackageBinaries() {
		entries["dist/"+name] = "BINARY-" + name
	}
	writeZip(t, archivePath, entries)
	body, err := os.ReadFile(archivePath) //nolint:gosec // G304: test-owned archive
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	mux := http.NewServeMux()
	mux.HandleFunc("/a.zip", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	mux.HandleFunc("/a.zip.sha256", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  a.zip\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	orig := binaryVersion
	t.Cleanup(func() { binaryVersion = orig })
	versionChecks := make(chan string, 2)
	releaseChecks := make(chan struct{})
	binaryVersion = func(path string) (string, error) {
		versionChecks <- filepath.Dir(path)
		<-releaseChecks
		return "build-777", nil
	}
	rel := Release{Tag: "build-777", AssetName: "a.zip", AssetURL: srv.URL + "/a.zip", SHAURL: srv.URL + "/a.zip.sha256"}

	type result struct {
		staged StagedInfo
		err    error
	}
	results := make(chan result, 2)
	started := make(chan struct{}, 2)
	for range 2 {
		go func() {
			started <- struct{}{}
			staged, err := Fetch(context.Background(), rel)
			results <- result{staged: staged, err: err}
		}()
	}
	<-started
	<-started
	firstDir := <-versionChecks
	select {
	case secondDir := <-versionChecks:
		t.Fatalf("concurrent Fetch bypassed the stage lease (%s and %s)", firstDir, secondDir)
	case <-time.After(250 * time.Millisecond):
	}
	close(releaseChecks)
	var publishedDirs []string
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatalf("Fetch: %v", got.err)
		}
		if _, err := os.Stat(got.staged.Dir); err != nil {
			t.Fatalf("published stage directory is unavailable: %v", err)
		}
		publishedDirs = append(publishedDirs, got.staged.Dir)
	}
	if publishedDirs[0] != publishedDirs[1] || publishedDirs[0] != firstDir {
		t.Fatalf("same release was published more than once: %v (downloaded %s)", publishedDirs, firstDir)
	}
	select {
	case extraDir := <-versionChecks:
		t.Fatalf("release was downloaded twice; extra stage %s", extraDir)
	default:
	}
}

func TestFetch_DoesNotPublishAfterChannelChanges(t *testing.T) {
	isolatedHome(t)
	if err := SaveState(State{Channel: ChannelBuild, Latest: &RelInfo{Tag: "build-888"}}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "onebase.zip")
	entries := map[string]string{}
	for _, name := range PackageBinaries() {
		entries["dist/"+name] = "BINARY-" + name
	}
	writeZip(t, archivePath, entries)
	body, err := os.ReadFile(archivePath) //nolint:gosec // G304: test-owned archive
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	mux := http.NewServeMux()
	mux.HandleFunc("/a.zip", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	mux.HandleFunc("/a.zip.sha256", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  a.zip\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	orig := binaryVersion
	t.Cleanup(func() { binaryVersion = orig })
	versionCheckStarted := make(chan struct{})
	releaseVersionCheck := make(chan struct{})
	binaryVersion = func(string) (string, error) {
		close(versionCheckStarted)
		<-releaseVersionCheck
		return "build-888", nil
	}
	rel := Release{Tag: "build-888", AssetName: "a.zip", AssetURL: srv.URL + "/a.zip", SHAURL: srv.URL + "/a.zip.sha256"}
	type fetchResult struct {
		staged StagedInfo
		err    error
	}
	fetchDone := make(chan fetchResult, 1)
	go func() {
		staged, err := Fetch(context.Background(), rel)
		fetchDone <- fetchResult{staged: staged, err: err}
	}()
	<-versionCheckStarted

	channelReserved := make(chan struct{})
	checkDone := make(chan error, 1)
	go func() {
		_, err := check(context.Background(), Options{Channel: ChannelStable}, func(context.Context, string, Channel) (Release, error) {
			close(channelReserved)
			return Release{Tag: "v2.0.0"}, nil
		})
		checkDone <- err
	}()
	<-channelReserved
	close(releaseVersionCheck)

	fetched := <-fetchDone
	if fetched.err == nil {
		t.Fatal("Fetch published an old-channel release after the channel reservation changed")
	}
	if fetched.staged.Dir != "" {
		if _, err := os.Stat(fetched.staged.Dir); !os.IsNotExist(err) {
			t.Fatalf("rejected stage directory was not cleaned up (err=%v)", err)
		}
	}
	if err := <-checkDone; err != nil {
		t.Fatalf("new-channel Check: %v", err)
	}
	got, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Channel != ChannelStable || got.Latest == nil || got.Latest.Tag != "v2.0.0" || got.Staged != nil {
		t.Fatalf("old Fetch contaminated new channel state: %+v", got)
	}
}
