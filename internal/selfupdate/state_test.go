package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
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
