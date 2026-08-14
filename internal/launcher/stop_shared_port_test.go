package launcher

// Жалоба пользователя (13.08.2026): две базы в реестре получили один и тот же
// порт 8080 (значение по умолчанию), после чего «Стоп всё» и ответ «Нет» в
// диалоге закрытия отказывали с «база X или её порт N заняты процессом без
// подтверждённого безопасного управления». Закрыть окно было нельзя вообще, а
// кнопки тулбара показывали текст ошибки вместо страницы — в нативном окне без
// адресной строки это тупик.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// foreignListener — посторонний процесс на порту: onebase-идентичность не
// подтверждается, но порт занят (у пользователя это была вторая база с тем же
// номером порта; для лаунчера это неотличимо от чужой программы на 8080).
func foreignListener(t *testing.T) int {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// sharedPortFixture — реестр из двух баз на одном порту, который занят
// неподтверждённым процессом. Ровно конфигурация из жалобы.
func sharedPortFixture(t *testing.T) (*handler, int) {
	t.Helper()
	port := foreignListener(t)
	st := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	legacy := []*Base{
		{ID: "tasks", Name: "Задачи", ConfigSource: "file", Path: t.TempDir(), Port: port, ControlToken: "tok-tasks"},
		{ID: "mini", Name: "MiniConf", ConfigSource: "file", Path: t.TempDir(), Port: port, ControlToken: "tok-mini"},
	}
	// Duplicate ports can exist in files written by older launchers. Keep this
	// compatibility fixture at the raw-document layer: new Add/Update mutations
	// now reject introducing the same invalid state.
	if err := st.mutateDocument(func(doc *yaml.Node) (bool, error) {
		return true, setStoreBases(doc, legacy)
	}); err != nil {
		t.Fatalf("write legacy shared-port registry: %v", err)
	}
	return &handler{store: st, runner: NewRunner()}, port
}

func TestStopAllBases_UnidentifiedPortIsWarningNotFailure(t *testing.T) {
	h, port := sharedPortFixture(t)

	skipped, err := h.stopAllBases(true)
	if err != nil {
		t.Fatalf("занятый чужим процессом порт заблокировал «Стоп всё»: %v", err)
	}
	if len(skipped) != 2 {
		t.Fatalf("обе базы должны вернуться пропущенными: %+v", skipped)
	}
	for _, base := range skipped {
		if base.Controllable {
			t.Errorf("пропущенная база помечена управляемой: %+v", base)
		}
	}
	if portFree(port) {
		t.Fatal("чужой процесс был убит по номеру порта")
	}
	text := skippedBasesText("ru", skipped)
	for _, want := range []string{"Задачи", "MiniConf", strconv.Itoa(port)} {
		if !strings.Contains(text, want) {
			t.Errorf("в предупреждении нет %q:\n%s", want, text)
		}
	}
}

// Спрашивать «оставить базы работать в фоне?» имеет смысл только про базы,
// которые лаунчер может остановить: у остальных ответ «Нет» ничего не меняет.
func TestCloseDecision_IgnoresBasesLauncherCannotStop(t *testing.T) {
	h, _ := sharedPortFixture(t)

	running, policy, err := h.closeState()
	if err != nil {
		t.Fatalf("closeState: %v", err)
	}
	if len(running) != 2 {
		t.Fatalf("в диалоге должны быть видны обе занятые базы: %+v", running)
	}
	if got := stoppableBases(running); got != 0 {
		t.Fatalf("остановить нечего, а насчитали %d", got)
	}
	if got := planForClose(policy, stoppableBases(running)); got != planKeepRunning {
		t.Fatalf("окно с одними неуправляемыми базами должно закрываться, план = %d", got)
	}
}

// Кнопка «Остановить все» в модалке закрытия раньше выключалась, стоило одной
// базе оказаться на занятом порту: пользователь терял и её тоже.
func TestCloseModal_KeepsStopButtonEnabledWithBlockedPort(t *testing.T) {
	h, _ := sharedPortFixture(t)
	rec := httptest.NewRecorder()
	h.index(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	if strings.Contains(body, "stopButton.disabled = hasBlocker") {
		t.Error("кнопка «Остановить все» снова блокируется неподтверждённым процессом")
	}
	if !strings.Contains(body, "stopButton.disabled = false") {
		t.Errorf("на странице нет разблокированной кнопки остановки:\n%s", body)
	}
}

// «Стоп всё» жмут навигацией: ответ обработчика становится страницей. Пока он
// отвечал текстом ошибки, окно лаунчера превращалось в белый экран без «Назад».
func TestKillAll_ReturnsToListWithBanner(t *testing.T) {
	h, _ := sharedPortFixture(t)

	rec := httptest.NewRecorder()
	h.killAll(rec, httptest.NewRequest(http.MethodPost, "/killall?sel=tasks", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("код %d, ожидался редирект на список: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "/?sel=tasks&flash=") {
		t.Fatalf("Location = %q: пользователь должен вернуться к списку баз", location)
	}

	// Класс ищем вместе с блоком: одноимённое правило есть и в CSS страницы.
	const warningBanner = `class="flash flash-warning"`
	page := httptest.NewRecorder()
	h.index(page, httptest.NewRequest(http.MethodGet, location, nil))
	body := page.Body.String()
	if !strings.Contains(body, warningBanner) || !strings.Contains(body, "MiniConf") {
		t.Fatalf("на странице нет предупреждения об оставшихся базах:\n%s", body)
	}

	// Сообщение одноразовое: F5 не должен повторять старый результат.
	again := httptest.NewRecorder()
	h.index(again, httptest.NewRequest(http.MethodGet, location, nil))
	if strings.Contains(again.Body.String(), warningBanner) {
		t.Error("баннер остался после перезагрузки страницы")
	}
}

func TestStopBase_FailureReturnsToListWithBanner(t *testing.T) {
	h, _ := sharedPortFixture(t)

	rec := httptest.NewRecorder()
	h.stop(rec, postForm(t, "tasks", "", ""))

	if rec.Code != http.StatusFound {
		t.Fatalf("код %d, ожидался редирект: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "/?sel=tasks&flash=") {
		t.Fatalf("Location = %q", location)
	}
	page := httptest.NewRecorder()
	h.index(page, httptest.NewRequest(http.MethodGet, location, nil))
	if !strings.Contains(page.Body.String(), `class="flash flash-error"`) {
		t.Fatalf("ошибка остановки не показана на странице:\n%s", page.Body.String())
	}
}

// Один и тот же порт у двух баз — первопричина жалобы: работать одновременно
// они не могут, а лаунчер отличает процессы по порту.
func TestCreateBase_RejectsPortOfAnotherBase(t *testing.T) {
	st := newTestStore(t)
	if err := st.Add(&Base{ID: "tasks", Name: "Задачи", ConfigSource: "file", Path: t.TempDir(), Port: 8080}); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: st}

	body := url.Values{
		"name": {"MiniConf"}, "config_source": {"file"}, "path": {t.TempDir()},
		"db_type": {"sqlite"}, "db_path": {filepath.Join(t.TempDir(), "mini.db")}, "port": {"8080"},
	}
	req := httptest.NewRequest(http.MethodPost, "/bases", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.create(rec, req)

	if !strings.Contains(rec.Body.String(), "Задачи") || !strings.Contains(rec.Body.String(), "8080") {
		t.Fatalf("конфликт порта не объяснён пользователю:\n%s", rec.Body.String())
	}
	bases, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(bases) != 1 {
		t.Fatalf("база с занятым портом всё-таки создана: %+v", bases)
	}
}

func TestUpdateBase_RejectsPortOfAnotherBase(t *testing.T) {
	st := newTestStore(t)
	for _, b := range []*Base{
		{ID: "tasks", Name: "Задачи", ConfigSource: "file", Path: t.TempDir(), DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "tasks.db"), Port: 8080},
		{ID: "mini", Name: "MiniConf", ConfigSource: "file", Path: t.TempDir(), DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "mini.db"), Port: 8081},
	} {
		if err := st.Add(b); err != nil {
			t.Fatal(err)
		}
	}
	h := &handler{store: st}

	body := url.Values{
		"name": {"MiniConf"}, "config_source": {"file"}, "path": {t.TempDir()},
		"db_type": {"sqlite"}, "db_path": {filepath.Join(t.TempDir(), "mini.db")}, "port": {"8080"},
	}
	rec := httptest.NewRecorder()
	h.update(rec, postForm(t, "mini", "", body.Encode()))

	if !strings.Contains(rec.Body.String(), "Задачи") {
		t.Fatalf("конфликт порта не объяснён пользователю:\n%s", rec.Body.String())
	}
	if got := storedBase(t, st, "mini"); got.Port != 8081 {
		t.Fatalf("порт базы изменён на конфликтующий: %d", got.Port)
	}
	// Собственный порт базы конфликтом не считается — иначе нельзя было бы
	// переименовать базу, не трогая порт.
	body.Set("port", "8081")
	rec = httptest.NewRecorder()
	h.update(rec, postForm(t, "mini", "", body.Encode()))
	if rec.Code != http.StatusFound {
		t.Fatalf("правка без смены порта отклонена: %d\n%s", rec.Code, rec.Body.String())
	}
}

// Форма новой базы предлагает свободный порт: иначе три базы подряд получают
// 8080 и конфликт создаётся сам собой.
func TestNewBaseForm_SuggestsFreePort(t *testing.T) {
	st := newTestStore(t)
	for _, b := range []*Base{
		{ID: "a", Name: "A", ConfigSource: "file", Path: t.TempDir(), Port: 8080},
		{ID: "b", Name: "B", ConfigSource: "file", Path: t.TempDir(), Port: 8081},
	} {
		if err := st.Add(b); err != nil {
			t.Fatal(err)
		}
	}
	h := &handler{store: st}
	rec := httptest.NewRecorder()
	h.newForm(rec, httptest.NewRequest(http.MethodGet, "/bases/new", nil))
	if !strings.Contains(rec.Body.String(), `value="8082"`) {
		t.Fatalf("форма предложила занятый порт:\n%s", rec.Body.String())
	}
}
