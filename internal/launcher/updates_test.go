package launcher

import (
	"net/http/httptest"
	"testing"

	"github.com/ivantit66/onebase/internal/selfupdate"
	"github.com/ivantit66/onebase/internal/version"
)

// isolatedUpdatesHome уводит ~/.onebase во временный каталог: тесты не должны
// трогать ни реестр баз, ни состояние обновлений пользователя.
func isolatedUpdatesHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
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

	if ApplyStagedOnStart() {
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

	if ApplyStagedOnStart() {
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

	ResumeAfterUpdate(store, NewRunner())

	st, err := selfupdate.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RestartBases) != 0 {
		t.Fatalf("список баз для восстановления не очищен: %v", st.RestartBases)
	}
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
	isolatedUpdatesHome(t)
	h := &handler{}
	w := httptest.NewRecorder()
	h.updatesApply(w, httptest.NewRequest("POST", "/updates/apply", nil))
	if w.Code != 409 {
		t.Fatalf("код %d, ждали 409 (нечего применять): %s", w.Code, w.Body.String())
	}
}
