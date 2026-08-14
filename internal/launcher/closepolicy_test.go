package launcher

// Тесты диалога закрытия окна информационных баз: когда спрашивать, что
// показывать, как запоминается ответ.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPlanForClose(t *testing.T) {
	cases := []struct {
		name    string
		policy  string
		running int
		want    closePlan
	}{
		{"нет живых баз — молча закрываем", OnCloseAsk, 0, planKeepRunning},
		{"нет живых баз, но stop-политика должна закрыть гонку со Start", OnCloseStop, 0, planStopAll},
		{"есть живые базы — спрашиваем", OnCloseAsk, 2, planAsk},
		{"пустая настройка = спрашиваем", "", 1, planAsk},
		{"мусор в настройке = спрашиваем", "junk", 1, planAsk},
		{"запомнили «в фоне»", OnCloseBackground, 3, planKeepRunning},
		{"запомнили «останавливать»", OnCloseStop, 1, planStopAll},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := planForClose(c.policy, c.running); got != c.want {
				t.Errorf("planForClose(%q, %d) = %d, ожидалось %d", c.policy, c.running, got, c.want)
			}
		})
	}
}

func TestPlanForRuntimeCloseWarnsWhenAskHasOnlyUnverifiedListeners(t *testing.T) {
	running := []RunningBase{{Name: "Foreign", Port: 8080, Controllable: false}}
	plan, warn := planForRuntimeClose(OnCloseAsk, running)
	if plan != planKeepRunning || !warn {
		t.Fatalf("ask + unverified-only decision = plan %d, warn %v", plan, warn)
	}
	if _, warn := planForRuntimeClose(OnCloseBackground, running); warn {
		t.Fatal("remembered background policy unexpectedly warns on every close")
	}
	if _, warn := planForRuntimeClose(OnCloseAsk, nil); warn {
		t.Fatal("empty runtime snapshot produced an unverified-process warning")
	}
}

// Кнопки системного MessageBox подписывает Windows, поэтому смысл каждой
// обязан быть в тексте — иначе «Нет» читается как «не закрывать».
func TestCloseDialogText_ExplainsButtons(t *testing.T) {
	text := closeDialogText("ru", []RunningBase{{Name: "Торговля", Port: 8080}, {Name: "Колл-центр", Port: 8081}})
	for _, want := range []string{"Торговля", "8080", "Колл-центр", "8081", "Да —", "Нет —", "Отмена —"} {
		if !strings.Contains(text, want) {
			t.Errorf("в тексте диалога нет %q:\n%s", want, text)
		}
	}
}

func TestCloseDialogText_ExplainsUncontrollablePort(t *testing.T) {
	text := closeDialogText("ru", []RunningBase{{Name: "Unknown", Port: 8080, Controllable: false}})
	if !strings.Contains(text, "автоматическая остановка недоступна") {
		t.Fatalf("диалог скрыл блокирующий процесс:\n%s", text)
	}
}

func TestCloseDialogText_LimitsLongList(t *testing.T) {
	var running []RunningBase
	for i := 0; i < maxDialogBases+5; i++ {
		running = append(running, RunningBase{Name: "База" + strconv.Itoa(i), Port: 8000 + i})
	}
	text := closeDialogText("ru", running)
	if strings.Contains(text, "База"+strconv.Itoa(maxDialogBases)) {
		t.Errorf("список баз должен обрезаться после %d — иначе вытеснит объяснение кнопок:\n%s", maxDialogBases, text)
	}
	if !strings.Contains(text, "5") {
		t.Errorf("должно быть сказано, сколько баз не поместилось:\n%s", text)
	}
}

// Язык нативного диалога брать неоткуда — HTTP-запроса в момент закрытия окна
// нет, поэтому он берётся от последней отрисованной страницы лаунчера.
func TestCurrentLang_FollowsLastRenderedPage(t *testing.T) {
	prev := currentLang()
	t.Cleanup(func() { rememberLang(prev) })

	if prev == "" {
		t.Fatal("язык по умолчанию не должен быть пустым — диалог остался бы без перевода")
	}
	rememberLang("en")
	if got := currentLang(); got != "en" {
		t.Errorf("после страницы на en ожидался en, получено %q", got)
	}
	// Пустой язык (страница без Lang) не должен затирать запомненный.
	rememberLang("")
	if got := currentLang(); got != "en" {
		t.Errorf("пустой язык затёр запомненный: %q", got)
	}
}

// closeFixture: реестр из двух баз — одна работает, вторая нет.
func closeFixture(t *testing.T) *handler {
	t.Helper()

	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(health.Close)
	u, _ := url.Parse(health.URL)
	port, _ := strconv.Atoi(u.Port())

	st := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	live := &Base{ID: "live", Name: "Торговля", ConfigSource: "file", Path: t.TempDir(), Port: port}
	idle := &Base{ID: "idle", Name: "Склад", ConfigSource: "file", Path: t.TempDir(), Port: waitReadyFreePort(t)}
	for _, b := range []*Base{live, idle} {
		if err := st.Add(b); err != nil {
			t.Fatalf("store.Add: %v", err)
		}
	}

	rn := NewRunner()
	rn.procs[live.ID] = &managedProc{port: port}
	return &handler{store: st, runner: rn}
}

func closeInfoReq(t *testing.T, h *handler) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.closeInfo(rec, httptest.NewRequest(http.MethodGet, "/close-info", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, ожидался no-store", got)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("разбор ответа: %v (%s)", err, rec.Body.String())
	}
	return got
}

func TestCloseInfo_ListsOnlyRunningBases(t *testing.T) {
	h := closeFixture(t)
	got := closeInfoReq(t, h)

	running, _ := got["running"].([]any)
	if len(running) != 1 {
		t.Fatalf("должна быть ровно одна работающая база: %v", got["running"])
	}
	first, _ := running[0].(map[string]any)
	if first["name"] != "Торговля" {
		t.Errorf("не та база в списке: %v", first)
	}
	if got["policy"] != OnCloseAsk {
		t.Errorf("по умолчанию лаунчер обязан спрашивать, получено %v", got["policy"])
	}
}

// Пустой ответ должен быть массивом, а не null: клиент читает running.length.
func TestCloseInfo_EmptyRunningIsArray(t *testing.T) {
	st := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	h := &handler{store: st, runner: NewRunner()}
	rec := httptest.NewRecorder()
	h.closeInfo(rec, httptest.NewRequest(http.MethodGet, "/close-info", nil))
	if !strings.Contains(rec.Body.String(), `"running":[]`) {
		t.Errorf("ожидался пустой массив running: %s", rec.Body.String())
	}
}

func TestCloseInfo_RegistryErrorIsNotReportedAsNoRunningBases(t *testing.T) {
	st := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	if err := os.WriteFile(st.path, []byte("bases: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: st, runner: NewRunner()}
	rec := httptest.NewRecorder()
	h.closeInfo(rec, httptest.NewRequest(http.MethodGet, "/close-info", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("битый registry обязан дать 500, получено %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"running":[]`) {
		t.Fatalf("ошибка registry замаскирована под отсутствие баз: %s", rec.Body.String())
	}
}

func TestCloseState_IgnoresStalePageStatusCache(t *testing.T) {
	h := closeFixture(t)
	h.statusCache = map[string]baseStatus{
		"live": {running: false, fetched: time.Now()},
	}
	running, _, err := h.closeState()
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 || running[0].Name != "Торговля" {
		t.Fatalf("close state переиспользовал stale UI cache: %+v", running)
	}
}

// Процесс, принадлежность которого не подтверждена, лаунчер не убивает — но и
// закрыться из-за него не отказывается: иначе окно нельзя закрыть, пока порт
// занят. Пользователь узнаёт об оставшейся базе из предупреждения.
func TestCloseStop_WaitsForUnidentifiedProcessWarningAcknowledgement(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Header().Set("X-OneBase-Version", "legacy")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(health.Close)
	u, _ := url.Parse(health.URL)
	port, _ := strconv.Atoi(u.Port())
	st := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	if err := st.Add(&Base{ID: "legacy", Name: "Старая", Port: port}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{quit: make(chan struct{})}
	var fallback func()
	srv.scheduleQuit = func(delay time.Duration, fn func()) {
		switch delay {
		case launcherWarningQuitFallback:
			fallback = fn
		case launcherQuitDelay:
			fn()
		default:
			t.Fatalf("unexpected quit delay: %s", delay)
		}
	}
	h := &handler{store: st, runner: NewRunner(), quitFn: srv.requestQuit, scheduleQuit: srv.after}
	srv.h = h
	rec := httptest.NewRecorder()
	h.closeStop(rec, httptest.NewRequest(http.MethodPost, "/close-stop", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("разбор ответа: %v (%s)", err, rec.Body.String())
	}
	warning, _ := got["warning"].(string)
	if !strings.Contains(warning, "Старая") {
		t.Fatalf("клиент не узнал, что база осталась работать: %q", warning)
	}
	select {
	case <-srv.Done():
		t.Fatal("launcher closed before the skipped-base warning was acknowledged")
	default:
	}
	if fallback == nil {
		t.Fatal("disconnected-client fallback was not scheduled")
	}
	ack := httptest.NewRecorder()
	srv.handleQuit(ack, httptest.NewRequest(http.MethodPost, "/quit", nil))
	if ack.Code != http.StatusOK {
		t.Fatalf("warning acknowledgement code %d: %s", ack.Code, ack.Body.String())
	}
	select {
	case <-srv.Done():
	default:
		t.Fatal("launcher did not close after warning acknowledgement")
	}
	if portFree(port) {
		t.Fatal("legacy-процесс был убит по номеру порта")
	}
}

func setPolicyReq(t *testing.T, h *handler, policy string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/close-policy", strings.NewReader("policy="+url.QueryEscape(policy)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.setClosePolicy(rec, req)
	return rec
}

func TestSetClosePolicy_PersistsChoice(t *testing.T) {
	h := closeFixture(t)

	if rec := setPolicyReq(t, h, OnCloseBackground); rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d: %s", rec.Code, rec.Body.String())
	}
	if got := h.onClosePolicy(); got != OnCloseBackground {
		t.Fatalf("настройка не сохранилась: %q", got)
	}
	if got := closeInfoReq(t, h)["policy"]; got != OnCloseBackground {
		t.Errorf("/close-info отдаёт старую настройку: %v", got)
	}

	// Реестр баз при этом не должен пострадать.
	bases, err := h.store.List()
	if err != nil || len(bases) != 2 {
		t.Fatalf("список баз потерян после сохранения настройки: %v / %v", bases, err)
	}
}

// Неизвестное значение — 400, а не молчаливая подмена: иначе клиент решит,
// что выбор сохранён.
func TestSetClosePolicy_RejectsUnknown(t *testing.T) {
	h := closeFixture(t)
	if rec := setPolicyReq(t, h, "во-фоне"); rec.Code != http.StatusBadRequest {
		t.Fatalf("ожидался 400, получен %d: %s", rec.Code, rec.Body.String())
	}
	if got := h.onClosePolicy(); got != OnCloseAsk {
		t.Errorf("настройка не должна была измениться: %q", got)
	}
}

// Настройка лаунчера переживает правки списка баз (save пишет файл целиком).
func TestStoreSettings_SurviveBaseWrites(t *testing.T) {
	st := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	if err := st.SetOnClose(OnCloseStop); err != nil {
		t.Fatalf("SetOnClose: %v", err)
	}
	if err := st.Add(&Base{ID: "b1", Name: "База", Port: 8080}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := st.Remove("b1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got, err := st.Settings()
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.OnClose != OnCloseStop {
		t.Errorf("настройка затёрта записью списка баз: %q", got.OnClose)
	}
}

// Страница лаунчера должна нести диалог закрытия и подтверждение «Стоп всё».
func TestIndex_RendersCloseDialog(t *testing.T) {
	h := closeFixture(t)
	rec := httptest.NewRecorder()
	h.index(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	for _, want := range []string{
		`id="close-modal"`,
		"closeChoice('background')",
		"closeChoice('stop')",
		"closeChoice('cancel')",
		"confirmKillAll(this)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("на странице нет %q", want)
		}
	}
	// Число работающих баз уезжает в JS: по нему решается, спрашивать ли перед
	// «Стоп всё». html/template обкладывает число пробелами — сравниваем по
	// схлопнутым пробелам.
	if squashed := strings.Join(strings.Fields(body), " "); !strings.Contains(squashed, "var _runningCount = 1 ;") {
		t.Errorf("на странице нет счётчика работающих баз (_runningCount = 1)")
	}
}
