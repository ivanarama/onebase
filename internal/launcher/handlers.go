package launcher

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/incident"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// normalizeSQLitePath приводит ввод пути к файлу SQLite к одному виду:
// если пользователь указал папку (явный слэш на конце, существующий каталог
// или путь без расширения) — добавляет «<имя базы>.db», как это делает кнопка
// выбора папки.
func normalizeSQLitePath(path, name string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return p
	}
	if strings.EqualFold(filepath.Ext(p), ".db") {
		return p
	}
	isDir := false
	switch {
	case strings.HasSuffix(p, `\`) || strings.HasSuffix(p, "/"):
		isDir = true
		p = strings.TrimRight(p, `\/`)
	case filepath.Ext(p) == "":
		isDir = true
	default:
		// Путь вводит администратор в форме регистрации базы — это его
		// собственный путь на его же машине, а не чужой ввод. Здесь только
		// определение «файл или каталог», без чтения содержимого.
		if st, err := os.Stat(p); err == nil && st.IsDir() { //nolint:gosec // G703: путь администратора, только Stat
			isDir = true
		}
	}
	if !isDir {
		return p
	}
	return filepath.Join(p, sanitizeFileName(name)+".db")
}

// sanitizeFileName убирает символы, недопустимые в именах файлов Windows.
// Должен совпадать с регуляркой в pickSQLiteDir() (templates.go).
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		out = "database"
	}
	return out
}

type handler struct {
	store         *Store
	runner        *Runner
	cfgLoginLimit *auth.LoginLimiter
	cfgLoginOnce  sync.Once
	// isoBrowser запускает изолированные окна Предприятия (план 78);
	// в тестах подменяется фейком.
	isoBrowser isolatedBrowser
	// quitFn просит лаунчер закрыться — нужен обновлению платформы, которое
	// заменяет бинарь и перезапускает процесс из нового файла (план 92).
	quitFn       func()
	scheduleQuit func(time.Duration, func())

	// incidents — последние ошибки самого лаунчера (план 116). Может быть nil:
	// часть тестов собирает handler литералом.
	incidents *incident.Store

	// statusCache кэширует ДОРОГИЕ на рендер списка проверки — статус живости
	// (усыновление через /health, до 1.5с) и данные app.yaml (открытие БД у
	// database-баз) — на короткий TTL. Без него каждое переключение базы
	// (`/?sel=`) синхронно и последовательно било по всем базам, и список
	// заметно тормозил (issue #596, регрессия c434500a). Мутирующие обработчики
	// (start/stop/…) сбрасывают запись базы, чтобы статус обновился сразу.
	statusMu    sync.Mutex
	statusCache map[string]baseStatus
	// updateMu serializes every selfupdate state mutation, including the quiet
	// watcher, so a stale network result cannot erase restart recovery state.
	updateMu sync.Mutex
	// updateQuiescing is permanent for this process. Once its on-disk binary
	// generation changed, the old in-memory launcher must reject all further
	// requests while handing off (or after a failed restart).
	updateQuiescing atomic.Bool
	// flashes — одноразовые сообщения списка баз (flash.go): результат операции,
	// выполненной навигацией, показывается баннером после редиректа.
	flashMu sync.Mutex
	flashes map[string]flashMessage
}

// baseStatus — закэшированный результат дорогих проверок одной базы.
type baseStatus struct {
	running    bool
	appName    string
	appVersion string
	hasLogo    bool
	fetched    time.Time
}

// baseStatusTTL — как долго переиспользуется закэшированный статус базы. Секунды:
// быстрое переключение `/?sel=` укладывается в окно и не перепробует, а лаг
// индикатора «запущена/остановлена» при этом незаметен (плюс мутирующие действия
// сбрасывают кэш сразу).
const baseStatusTTL = 3 * time.Second

// baseVM — view-модель информационной базы для списка лаунчера: встраивает
// *Base и дополняет рантайм-полями (запущена ли база, URL, данные из app.yaml).
type baseVM struct {
	*Base
	Running    bool
	BaseURL    string
	AppName    string
	AppVersion string
	LogoBase64 string
}

// baseStatuses возвращает статусы всех баз, обновляя протухшие записи кэша
// ПАРАЛЛЕЛЬНО и с общим TTL. Раньше рендер списка синхронно и последовательно
// пробовал каждую базу (/health до 1.5с + открытие БД у database-баз), и цена
// линейно росла с числом баз, повторяясь на каждом `/?sel=` (issue #596).
func (h *handler) baseStatuses(bases []*Base) map[string]baseStatus {
	now := time.Now()
	h.statusMu.Lock()
	if h.statusCache == nil {
		h.statusCache = map[string]baseStatus{}
	}
	var stale []*Base
	for _, b := range bases {
		if st, ok := h.statusCache[b.ID]; !ok || now.Sub(st.fetched) > baseStatusTTL {
			stale = append(stale, b)
		}
	}
	h.statusMu.Unlock()

	if len(stale) > 0 {
		fresh := make([]baseStatus, len(stale))
		var wg sync.WaitGroup
		for i, b := range stale {
			wg.Add(1)
			go func(i int, b *Base) {
				defer wg.Done()
				fresh[i] = h.probeBase(b)
			}(i, b)
		}
		wg.Wait()
		h.statusMu.Lock()
		for i, b := range stale {
			h.statusCache[b.ID] = fresh[i]
		}
		h.statusMu.Unlock()
	}

	out := make(map[string]baseStatus, len(bases))
	h.statusMu.Lock()
	for _, b := range bases {
		out[b.ID] = h.statusCache[b.ID]
	}
	h.statusMu.Unlock()
	return out
}

// probeBase выполняет дорогие проверки одной базы — статус живости и данные
// app.yaml. Вызывается из горутины baseStatuses, поэтому без общих блокировок.
func (h *handler) probeBase(b *Base) baseStatus {
	gate := cfgAuthDBGate(b.ID)
	gate.RLock()
	defer gate.RUnlock()
	st := baseStatus{running: h.baseRunning(b), fetched: time.Now()}
	var cfg struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
		Logo    string `yaml:"logo"`
	}
	// Одна сломанная конфигурация не должна ломать весь список: строка остаётся
	// пустой, причина уходит в журнал внутри readAppYAML.
	if err := readAppYAML(context.Background(), b, &cfg); err == nil {
		st.appName = cfg.Name
		st.appVersion = cfg.Version
		st.hasLogo = cfg.Logo != ""
	}
	return st
}

// invalidateStatus сбрасывает закэшированный статус базы — после старта/останова
// индикатор в списке должен обновиться сразу, не дожидаясь истечения TTL.
func (h *handler) invalidateStatus(baseID string) {
	h.statusMu.Lock()
	delete(h.statusCache, baseID)
	h.statusMu.Unlock()
}

// clearStatus сбрасывает весь кэш статусов (например, после «Стоп всё»).
func (h *handler) clearStatus() {
	h.statusMu.Lock()
	h.statusCache = map[string]baseStatus{}
	h.statusMu.Unlock()
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	bases, err := h.store.List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	statuses := h.baseStatuses(bases)

	selID := r.URL.Query().Get("sel")
	var selected *baseVM
	runningCount := 0
	vms := make([]*baseVM, 0, len(bases))
	for _, b := range bases {
		st := statuses[b.ID]
		vm := &baseVM{Base: b, Running: st.running, BaseURL: h.runner.BaseURL(b),
			AppName: st.appName, AppVersion: st.appVersion}
		if st.hasLogo {
			vm.LogoBase64 = "/bases/" + b.ID + "/configurator/logo"
		}
		vms = append(vms, vm)
		if vm.Running {
			runningCount++
		}
		if b.ID == selID {
			selected = vm
		}
	}
	if selected == nil && len(vms) > 0 {
		selected = vms[0]
	}

	flash, _ := h.takeFlash(r.URL.Query().Get("flash"))
	render(w, r, "page-index", map[string]any{
		"Title":        tr(resolveLang(r), "onebase — Информационные базы"),
		"Bases":        vms,
		"Selected":     selected,
		"NativeOK":     NativeIsolatedSupported(),
		"RunningCount": runningCount,
		"ClosePolicy":  h.onClosePolicy(),
		"FlashKind":    flash.kind,
		"FlashText":    flash.text,
		// Состояние обновлений читается из файла, без обращения к сети:
		// проверку делает фоновая горутина (план 92).
		"Update": h.updatesState(),
		"BaseURL": func() string {
			if selected != nil {
				return h.runner.BaseURL(selected.Base)
			}
			return ""
		}(),
	})
}

func (h *handler) newForm(w http.ResponseWriter, r *http.Request) {
	port := defaultBasePort
	if bases, err := h.store.List(); err == nil {
		port = freeRegistryPort(bases)
	}
	render(w, r, "page-form", map[string]any{
		"Title": tr(resolveLang(r), "onebase — Добавить базу"),
		"IsNew": true,
		"Base":  &Base{ConfigSource: "file", DBType: "sqlite", Port: port},
		"Error": "",
	})
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if failForm(w, r) {
		return
	}
	lang := resolveLang(r)
	dbType := r.FormValue("db_type")
	if dbType == "" {
		dbType = "postgres"
	}
	b := &Base{
		Name:         r.FormValue("name"),
		ConfigSource: r.FormValue("config_source"),
		Path:         r.FormValue("path"),
		DB:           r.FormValue("db"),
		DBType:       dbType,
		DBPath:       r.FormValue("db_path"),
		Port:         parsePort(r.FormValue("port")),
		Host:         normalizeHost(r.FormValue("host")),
	}

	if b.Name == "" {
		render(w, r, "page-form", map[string]any{
			"Title": tr(lang, "onebase — Добавить базу"),
			"IsNew": true, "Base": b, "Error": tr(lang, "Наименование обязательно"),
		})
		return
	}
	if b.DBType == "sqlite" {
		b.DBPath = normalizeSQLitePath(b.DBPath, b.Name)
	}
	if b.DBType == "sqlite" && b.DBPath == "" {
		render(w, r, "page-form", map[string]any{
			"Title": tr(lang, "onebase — Добавить базу"),
			"IsNew": true, "Base": b, "Error": tr(lang, "Укажите путь к файлу SQLite"),
		})
		return
	}
	if b.DBType != "sqlite" && b.DB == "" {
		render(w, r, "page-form", map[string]any{
			"Title": tr(lang, "onebase — Добавить базу"),
			"IsNew": true, "Base": b, "Error": tr(lang, "Укажите строку подключения к PostgreSQL"),
		})
		return
	}
	if bases, err := h.store.List(); err == nil {
		if owner := portOwner(bases, "", b.Port); owner != nil {
			render(w, r, "page-form", map[string]any{
				"Title": tr(lang, "onebase — Добавить базу"),
				"IsNew": true, "Base": b, "Error": portConflictError(lang, owner, bases),
			})
			return
		}
	}
	// Creating a database-backed registration may open or initialize the same
	// database that a restore is replacing. Share the global lifecycle gate so
	// the alias preflight and the destructive restore cannot be bypassed by a
	// concurrent create request.
	if h.runner != nil {
		if err := h.runner.holdStarts(); err != nil {
			render(w, r, "page-form", map[string]any{
				"Title": tr(lang, "onebase — Добавить базу"), "IsNew": true, "Base": b,
				"Error": tr(lang, "Другая операция с базами ещё выполняется") + ": " + err.Error(),
			})
			return
		}
		defer h.runner.AllowStarts()
	}

	scaffold := r.FormValue("scaffold") == "1"

	if b.ConfigSource == "database" {
		if err := h.initDatabaseBase(r.Context(), b, scaffold); err != nil {
			render(w, r, "page-form", map[string]any{
				"Title": tr(lang, "onebase — Добавить базу"),
				"IsNew": true, "Base": b, "Error": errText(r, err),
			})
			return
		}
	} else {
		// file mode
		if b.Path == "" {
			render(w, r, "page-form", map[string]any{
				"Title": tr(lang, "onebase — Добавить базу"),
				"IsNew": true, "Base": b, "Error": tr(lang, "Укажите путь к папке конфигурации"),
			})
			return
		}
		if scaffold {
			if err := os.MkdirAll(b.Path, fsmode.Dir); err != nil { //nolint:gosec // G703: путь получен обходом каталога проекта (os.ReadDir/WalkDir), из запроса он не приходит
				render(w, r, "page-form", map[string]any{
					"Title": tr(lang, "onebase — Добавить базу"),
					"IsNew": true, "Base": b, "Error": tr(lang, "Не удалось создать папку") + ": " + err.Error(),
				})
				return
			}
			if err := project.Scaffold(b.Path, b.Name); err != nil {
				render(w, r, "page-form", map[string]any{
					"Title": tr(lang, "onebase — Добавить базу"),
					"IsNew": true, "Base": b, "Error": tr(lang, "Ошибка создания конфигурации") + ": " + err.Error(),
				})
				return
			}
		}
		// PG базу создаём только для PG. Для SQLite файл создаётся при первом
		// ConnectSQLite — здесь делать ничего не надо.
		if b.DBType != "sqlite" {
			if err := storage.EnsureDatabase(r.Context(), b.DB); err != nil {
				render(w, r, "page-form", map[string]any{
					"Title": tr(lang, "onebase — Добавить базу"),
					"IsNew": true, "Base": b, "Error": tr(lang, "Не удалось создать БД") + ": " + err.Error(),
				})
				return
			}
		}
	}

	if err := h.store.Add(b); err != nil {
		if message, ok := storedPortConflictError(lang, err); ok {
			render(w, r, "page-form", map[string]any{
				"Title": tr(lang, "onebase — Добавить базу"),
				"IsNew": true, "Base": b, "Error": message,
			})
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/?sel="+b.ID, http.StatusFound)
}

func (h *handler) editForm(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	render(w, r, "page-form", map[string]any{
		"Title": tr(resolveLang(r), "onebase — Изменить базу"),
		"IsNew": false, "Base": b, "Error": "",
	})
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if failForm(w, r) {
		return
	}
	b.Name = r.FormValue("name")
	b.ConfigSource = r.FormValue("config_source")
	b.Path = r.FormValue("path")
	b.DB = r.FormValue("db")
	if dt := r.FormValue("db_type"); dt != "" {
		b.DBType = dt
	}
	b.DBPath = r.FormValue("db_path")
	b.Port = parsePort(r.FormValue("port"))
	b.Host = normalizeHost(r.FormValue("host"))

	if b.Name == "" {
		render(w, r, "page-form", map[string]any{
			"Title": tr(lang, "onebase — Изменить базу"),
			"IsNew": false, "Base": b, "Error": tr(lang, "Наименование обязательно"),
		})
		return
	}
	if b.DBType == "sqlite" {
		b.DBPath = normalizeSQLitePath(b.DBPath, b.Name)
	}
	if b.DBType == "sqlite" && b.DBPath == "" {
		render(w, r, "page-form", map[string]any{
			"Title": tr(lang, "onebase — Изменить базу"),
			"IsNew": false, "Base": b, "Error": tr(lang, "Укажите путь к файлу SQLite"),
		})
		return
	}
	if b.DBType != "sqlite" && b.DB == "" {
		render(w, r, "page-form", map[string]any{
			"Title": tr(lang, "onebase — Изменить базу"),
			"IsNew": false, "Base": b, "Error": tr(lang, "Укажите строку подключения к PostgreSQL"),
		})
		return
	}
	if bases, err := h.store.List(); err == nil {
		if owner := portOwner(bases, b.ID, b.Port); owner != nil {
			render(w, r, "page-form", map[string]any{
				"Title": tr(lang, "onebase — Изменить базу"),
				"IsNew": false, "Base": b, "Error": portConflictError(lang, owner, bases),
			})
			return
		}
	}
	releaseDB := acquireCfgDBExclusive(b.ID)
	defer releaseDB()
	if h.runner != nil {
		if err := h.runner.holdStarts(); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		defer h.runner.AllowStarts()
	}
	current, err := h.store.Get(b.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if h.runner != nil && runtimeConfigChanged(current, b) && h.runner.RuntimeStatus(current).Occupied {
		render(w, r, "page-form", map[string]any{
			"Title": tr(lang, "onebase — Изменить базу"), "IsNew": false, "Base": b,
			"Error": tr(lang, "Сначала остановите базу: параметры запуска нельзя менять у работающего процесса"),
		})
		return
	}
	// Preserve fields that are not editable by this form and may have changed
	// while it was open (notably LastOpened and the lifecycle control secret).
	b.ID = current.ID
	b.ControlToken = current.ControlToken
	b.Created = current.Created
	b.LastOpened = current.LastOpened
	err = h.store.Update(b)
	if err != nil {
		if message, ok := storedPortConflictError(lang, err); ok {
			render(w, r, "page-form", map[string]any{
				"Title": tr(lang, "onebase — Изменить базу"),
				"IsNew": false, "Base": b, "Error": message,
			})
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/?sel="+b.ID, http.StatusFound)
}

func runtimeConfigChanged(a, b *Base) bool {
	if a == nil || b == nil {
		return true
	}
	return a.ConfigSource != b.ConfigSource || a.Path != b.Path || a.DB != b.DB ||
		a.DBType != b.DBType || a.DBPath != b.DBPath || a.Port != b.Port || a.Host != b.Host
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	releaseDB := acquireCfgDBExclusive(id)
	defer releaseDB()
	if err := h.runner.holdStarts(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	defer h.runner.AllowStarts()
	b, err := h.store.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := h.runner.stopBaseHeld(b); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	// Сбой удаления нельзя проглатывать: редирект на список выглядит как
	// выполненное удаление, а база остаётся в реестре — пользователь решит, что
	// интерфейс завис, и нажмёт «удалить» ещё раз.
	if err := h.store.Remove(id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h.invalidateStatus(id) // база удалена — убираем осиротевшую запись кэша
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *handler) move(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	delta := 1
	if r.URL.Query().Get("dir") == "up" {
		delta = -1
	}
	if err := h.store.Move(id, delta); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/?sel="+id, http.StatusFound)
}

// baseRunning: база запущена этим лаунчером ИЛИ уже отвечает на своём порту
// (запущена прежним экземпляром лаунчера — например, после пересборки exe).
// Живую базу «усыновляем», а не требуем убивать вручную из-за «порт занят».
func (h *handler) baseRunning(b *Base) bool {
	if h.runner.IsRunning(b.ID) {
		return true
	}
	return !portFree(b.Port) && h.runner.Healthy(b)
}

// ensureBaseReady запускает базу, если она ещё не запущена, и ждёт готовности
// её сервера. Общий пролог обработчиков start / startIsolated / startNative:
// при ошибке пишет JSON-ответ и возвращает false.
func (h *handler) ensureBaseReady(w http.ResponseWriter, r *http.Request, b *Base, lang string) bool {
	// Mint the persistent identity before the first liveness/adoption probe.
	// Public /health on a tokenless legacy record is forgeable by any process
	// that won the saved port; treating that response as this base would hand
	// the browser the foreign listener's URL. A pre-token onebase process must
	// therefore be stopped manually once, after which every generation proves
	// its HMAC identity.
	if b.ControlToken == "" {
		token, err := h.store.EnsureControlToken(b.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": errText(r, err)})
			return false
		}
		b.ControlToken = token
	}
	if !h.baseRunning(b) {
		if b.DBType != "sqlite" {
			if err := storage.EnsureDatabase(r.Context(), b.DB); err != nil {
				writeJSON(w, 500, map[string]any{"error": tr(lang, "Не удалось создать БД") + ": " + err.Error()})
				return false
			}
		}
		if err := h.runner.Start(b); err != nil {
			h.startFailure(w, r, b, err)
			return false
		}
		h.invalidateStatus(b.ID) // статус в списке должен обновиться сразу
		// База уже запущена — отказывать пользователю из-за несохранённой
		// отметки времени неправильно. Но сбой записи реестра означает, что не
		// сохранится и всё остальное, поэтому Warn, а не тишина.
		if err := h.store.TouchLastOpened(b.ID, time.Now()); err != nil {
			respondLog().Warn("не удалось сохранить отметку последнего открытия базы",
				"baseID", b.ID, "err", err)
		}
	}
	// Wait until the base server is ready before handing the URL to the client.
	if err := h.runner.WaitReady(b, 15*time.Second); err != nil {
		// Именно сюда попадает упавшая миграция: процесс завершился до открытия
		// порта, и в ошибке лежит хвост его лога с настоящей причиной.
		h.startFailure(w, r, b, err)
		return false
	}
	return true
}

func (h *handler) start(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	if !h.ensureBaseReady(w, r, b, resolveLang(r)) {
		return
	}
	writeJSON(w, 200, map[string]any{"url": h.runner.BaseURL(b)})
}

// startNative (кнопка «Предприятие» в GUI-сборке под Windows): запускает базу и
// открывает её в нативном WebView2-окне на ОБЩЕМ профиле — окно без адресной
// строки, в отличие от window.open, который в WebView2 убегает во внешний
// браузер. Пустой профиль (не изолированный) = единый cookie-jar с лаунчером,
// т.е. обычный сеанс Предприятия, а не свежий вход под другим пользователем
// («Новое окно»). В не-GUI-сборках isoBrowser.Open вернёт понятную ошибку, но
// UI туда и не ходит — при NativeOK=false кнопка открывает браузер.
func (h *handler) startNative(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	if !h.ensureBaseReady(w, r, b, resolveLang(r)) {
		return
	}
	if err := h.isoBrowser.Open("", h.runner.BaseURL(b), isolatedModeNative); err != nil {
		writeJSON(w, 500, map[string]any{"error": errText(r, err)})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// startIsolated (план 78, фаза 3): запускает базу (если нужно) и открывает
// внешнее Chromium-окно с изолированным браузерным профилем. Сессионный токен
// не передаётся: свежий профиль без cookie попадает на /login — это и есть
// смысл «окна под другого пользователя».
func (h *handler) startIsolated(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	if !h.ensureBaseReady(w, r, b, resolveLang(r)) {
		return
	}

	mode := r.URL.Query().Get("mode")
	switch mode {
	case "", isolatedModeAuto, isolatedModeNative, isolatedModeBrowser:
	default:
		writeJSON(w, 400, map[string]any{"error": "неизвестный режим: " + mode})
		return
	}

	root, err := profilesRoot(b.ID)
	if err == nil {
		var dir string
		if dir, err = pickProfileDir(root); err == nil {
			err = h.isoBrowser.Open(dir, h.runner.BaseURL(b)+"/ui", mode)
		}
	}
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": errText(r, err)})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// cleanProfiles удаляет свободные (не запущенные) изолированные профили базы.
func (h *handler) cleanProfiles(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	root, err := profilesRoot(b.ID)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	removed, err := cleanIsolatedProfiles(root)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "removed": removed})
}

func (h *handler) stop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	b, err := h.store.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := h.runner.StopBase(b); err != nil {
		// Кнопку жмут навигацией: текст ошибки страницей превратил бы окно
		// лаунчера в тупик — в нативном окне нет ни адресной строки, ни «Назад».
		h.redirectWithFlash(w, r, "/?sel="+id, flashError, err.Error())
		return
	}
	h.invalidateStatus(id) // индикатор «остановлена» — сразу, не через TTL
	http.Redirect(w, r, "/?sel="+id, http.StatusFound)
}

func (h *handler) killAll(w http.ResponseWriter, r *http.Request) {
	sel := r.URL.Query().Get("sel")
	redirect := "/"
	if sel != "" {
		redirect = "/?sel=" + sel
	}

	skipped, err := h.stopAllBases(false)
	if err != nil {
		h.redirectWithFlash(w, r, redirect, flashError, err.Error())
		return
	}
	if len(skipped) != 0 {
		h.redirectWithFlash(w, r, redirect, flashWarning, skippedBasesText(resolveLang(r), skipped))
		return
	}
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (h *handler) configuratorMigrate(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	out, runErr := h.runner.MigrateBase(r.Context(), b)
	w.Header().Set("Content-Type", "application/json")
	if runErr != nil {
		respondJSONTo(w, map[string]any{"output": out, "error": runErr.Error()})
		return
	}
	respondJSONTo(w, map[string]any{"output": out, "error": ""})
}

// configuratorReorder сохраняет пользовательский порядок объектов одной группы
// дерева (ручное перемещение, как в 1С). Тело: group=<ключ>, name=<имя> (повтор).
func (h *handler) configuratorReorder(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Клиент шлёт FormData (multipart/form-data). Нельзя ограничиться ParseForm:
	// для multipart он не читает тело, а после него FormValue/r.Form уже не
	// триггерят ParseMultipartForm (r.Form != nil) → group и name приходят пустыми.
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil && err != http.ErrNotMultipart {
		writeJSON(w, requestBodyErrorStatus(err), map[string]any{"ok": false, "error": err.Error()})
		return
	}
	group := r.FormValue("group")
	// "groups" — спец-ключ: порядок самих групп дерева.
	if group != "groups" && !treeOrderGroups[group] {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Неизвестная группа: " + group})
		return
	}
	names := r.Form["name"]
	if err := h.saveTreeOrderGroupFor(r.Context(), b, group, names); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Подсистемы в пользовательском режиме сортируются по полю order, а не по
	// tree_order.yaml — поэтому при их перетаскивании синхронизируем order, чтобы
	// порядок совпал и в Предприятии.
	if group == "subsystems" {
		h.applySubsystemOrder(r.Context(), b, names)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// configuratorLaunchState сообщает, нужно ли при запуске Предприятия предложить
// обновить БД: запущена ли база и изменилась ли конфигурация с момента последней
// миграции (аналог проверки реструктуризации в 1С при F5).
func (h *handler) configuratorLaunchState(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	configChanged := false
	if b.ConfigSource == "file" {
		if t, ok := migratedAt(b.ID); ok {
			configChanged = configDirtyAfter(b.Path, t)
		} else {
			// БД ещё ни разу не синхронизирована из этой инсталляции лаунчера.
			configChanged = true
		}
	}
	writeJSON(w, 200, map[string]any{
		"running":       h.baseRunning(b),
		"configChanged": configChanged,
	})
}

// configuratorRestart останавливает и заново запускает базу, чтобы запущенная
// сессия Предприятия подхватила изменения конфигурации.
func (h *handler) configuratorRestart(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	lang := resolveLang(r)
	if b.ControlToken == "" {
		token, tokenErr := h.store.EnsureControlToken(b.ID)
		if tokenErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": errText(r, tokenErr)})
			return
		}
		b.ControlToken = token
	}
	if b.DBType != "sqlite" {
		if err := storage.EnsureDatabase(r.Context(), b.DB); err != nil {
			writeJSON(w, 500, map[string]any{"error": tr(lang, "Не удалось создать БД") + ": " + err.Error()})
			return
		}
	}
	if err := h.runner.Restart(b); err != nil {
		writeJSON(w, 500, map[string]any{"error": errText(r, err)})
		return
	}
	if err := h.store.TouchLastOpened(b.ID, time.Now()); err != nil {
		respondLog().Warn("не удалось сохранить отметку последнего открытия базы",
			"baseID", b.ID, "err", err)
	}
	if err := h.runner.WaitReady(b, 15*time.Second); err != nil {
		writeJSON(w, 500, map[string]any{"error": errText(r, err)})
		return
	}
	writeJSON(w, 200, map[string]any{"url": h.runner.BaseURL(b)})
}

func (h *handler) configExport(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	backURL := "/bases/" + b.ID + "/configurator?tab=files"
	if b.ConfigSource != "database" {
		render(w, r, "page-config-result", map[string]any{
			"Title":   tr(lang, "onebase — Конфигуратор"),
			"Message": tr(lang, "Выгрузка доступна только для баз в режиме «В базе данных»."),
			"Error":   "",
			"BackURL": backURL,
		})
		return
	}

	db, err := OpenDB(r.Context(), b)
	if err != nil {
		render(w, r, "page-config-result", map[string]any{
			"Title": tr(lang, "onebase — Конфигуратор"), "Message": "",
			"Error":   tr(lang, "Ошибка подключения") + ": " + err.Error(),
			"BackURL": backURL,
		})
		return
	}
	defer db.Close()

	workDir, err := workspacePath(b.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	repo := configdb.New(db)
	if err := repo.ExportToDir(r.Context(), workDir); err != nil {
		render(w, r, "page-config-result", map[string]any{
			"Title": tr(lang, "onebase — Конфигуратор"), "Message": "",
			"Error":   tr(lang, "Ошибка выгрузки") + ": " + err.Error(),
			"BackURL": backURL,
		})
		return
	}

	// Папка выгружена в любом случае — путь показан ниже. Не открылась —
	// пусть будет видно, почему.
	bestEffort("открыть папку выгрузки", OpenPath(workDir))

	render(w, r, "page-config-result", map[string]any{
		"Title":   tr(lang, "onebase — Конфигуратор"),
		"Message": fmt.Sprintf(tr(lang, "Конфигурация выгружена в папку")+": %s", workDir),
		"Error":   "",
		"BackURL": backURL,
	})
}

// validateConfigImportDir rejects an empty/non-project directory before
// configdb.ImportFromDir starts its replace-all transaction. In particular,
// an untouched ~/.onebase/workspace/<base-id> must not be accepted as a valid
// zero-file configuration.
func validateConfigImportDir(srcDir string) error {
	// srcDir — каталог, выбранный администратором в диалоге импорта на его
	// машине. Проверяем, что это папка проекта, до replace-all транзакции.
	info, err := os.Stat(srcDir) //nolint:gosec // G703: путь администратора
	if err != nil {
		return fmt.Errorf("папка конфигурации недоступна: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("путь не является папкой: %s", srcDir)
	}
	appPath := filepath.Join(srcDir, "config", "app.yaml")
	if info, err := os.Stat(appPath); err != nil || info.IsDir() { //nolint:gosec // G703: appPath собран из того же каталога администратора
		return fmt.Errorf("папка не содержит config/app.yaml: %s", srcDir)
	}
	cfg, err := project.LoadConfig(srcDir)
	if err != nil {
		return fmt.Errorf("ошибка config/app.yaml: %w", err)
	}
	if strings.TrimSpace(cfg.Name) == "" {
		return fmt.Errorf("в config/app.yaml не заполнено поле name")
	}
	return nil
}

func (h *handler) configImport(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	backURL := "/bases/" + b.ID + "/configurator?tab=files"

	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if failForm(w, r) {
		return
	}
	srcDir := strings.TrimSpace(r.FormValue("path"))
	if srcDir == "" {
		srcDir, err = workspacePath(b.ID)
		if err != nil {
			render(w, r, "page-config-result", map[string]any{
				"Title": tr(lang, "onebase — Загрузка конфигурации"), "Message": "",
				"Error": tr(lang, "Ошибка загрузки") + ": " + err.Error(), "BackURL": backURL,
			})
			return
		}
	}
	if err := validateConfigImportDir(srcDir); err != nil {
		render(w, r, "page-config-result", map[string]any{
			"Title": tr(lang, "onebase — Загрузка конфигурации"), "Message": "",
			"Error": tr(lang, "Ошибка загрузки") + ": " + err.Error(), "BackURL": backURL,
		})
		return
	}

	db, err := OpenDB(r.Context(), b)
	if err != nil {
		render(w, r, "page-config-result", map[string]any{
			"Title": tr(lang, "onebase — Загрузка конфигурации"), "Message": "",
			"Error":   tr(lang, "Ошибка подключения") + ": " + err.Error(),
			"BackURL": backURL,
		})
		return
	}
	defer db.Close()

	repo := configdb.New(db)
	if err := repo.ImportFromDir(r.Context(), srcDir); err != nil {
		render(w, r, "page-config-result", map[string]any{
			"Title": tr(lang, "onebase — Загрузка конфигурации"), "Message": "",
			"Error":   tr(lang, "Ошибка загрузки") + ": " + err.Error(),
			"BackURL": backURL,
		})
		return
	}
	if _, err := repo.CreateVersion(r.Context(), configdb.VersionOptions{
		AuthorLogin: cfgLogin(r.Context()),
		Message:     "import from " + srcDir,
	}); err != nil {
		render(w, r, "page-config-result", map[string]any{
			"Title": tr(lang, "onebase — Загрузка конфигурации"), "Message": "",
			"Error":   tr(lang, "Ошибка версии конфигурации") + ": " + err.Error(),
			"BackURL": backURL,
		})
		return
	}

	// Migrate after import
	out, _ := h.runner.MigrateBase(r.Context(), b)
	render(w, r, "page-config-result", map[string]any{
		"Title":   tr(lang, "onebase — Загрузка конфигурации"),
		"Message": fmt.Sprintf(tr(lang, "Конфигурация загружена из")+": %s\n\n"+tr(lang, "Миграция")+":\n%s", srcDir, out),
		"Error":   "",
		"BackURL": backURL,
	})
}

func (h *handler) initDatabaseBase(ctx context.Context, b *Base, scaffold bool) error {
	if b.DBType != "sqlite" {
		if err := storage.EnsureDatabase(ctx, b.DB); err != nil {
			return i18nerr.Wrapf(err, "создание БД")
		}
	}
	db, err := OpenDB(ctx, b)
	if err != nil {
		return i18nerr.Wrapf(err, "подключение к БД")
	}
	defer db.Close()

	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		return i18nerr.Wrapf(err, "создание схемы configdb")
	}

	if scaffold {
		name := b.Name
		if name == "" {
			name = "myapp"
		}
		tmpDir, err := os.MkdirTemp("", "onebase-scaffold-")
		if err != nil {
			return err
		}
		defer removeTemp(tmpDir)

		if err := project.Scaffold(tmpDir, name); err != nil {
			return i18nerr.Wrapf(err, "создание конфигурации")
		}
		if err := repo.ImportFromDir(ctx, tmpDir); err != nil {
			return i18nerr.Wrapf(err, "загрузка конфигурации")
		}
		if _, err := repo.CreateVersion(ctx, configdb.VersionOptions{Message: "initial scaffold"}); err != nil {
			return i18nerr.Wrapf(err, "снимок конфигурации")
		}
	}
	return nil
}

func workspacePath(baseID string) (string, error) {
	p, err := OnebasePath("workspace", baseID)
	if err != nil {
		return "", err
	}
	return p, os.MkdirAll(p, fsmode.Dir)
}

func resolveLang(r *http.Request) string {
	if launcherBundle != nil {
		// baseLang пустой: иначе Resolve возвращает его сразу и до
		// Accept-Language не доходит (issue #49 п.1); фолбэк "ru" встроен
		// в Resolve.
		return i18n.Resolve("", "", r.Header.Get("Accept-Language"), launcherBundle)
	}
	return "ru"
}

func tr(lang, key string) string {
	if launcherBundle != nil {
		return launcherBundle.T(lang, key)
	}
	return key
}

// errText локализует сообщение об ошибке для языка текущего запроса.
func errText(r *http.Request, err error) string {
	return i18nerr.Localize(launcherBundle, resolveLang(r), err)
}

func render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	if _, ok := data["Lang"]; !ok {
		data["Lang"] = resolveLang(r)
	}
	if lang, ok := data["Lang"].(string); ok {
		// Нативный диалог закрытия окна рисуется вне HTTP-запроса — язык он
		// берёт отсюда (см. currentLang).
		rememberLang(lang)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	respondJSONTo(w, v)
}

func (h *handler) browseDir(w http.ResponseWriter, r *http.Request) {
	title := r.URL.Query().Get("title")
	if title == "" {
		title = tr(resolveLang(r), "Выберите папку")
	}
	initialPath := r.URL.Query().Get("initial_path")
	path, err := BrowseDir(title, initialPath)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"path": path})
}

func (h *handler) browseFile(w http.ResponseWriter, r *http.Request) {
	lang := resolveLang(r)
	title := r.URL.Query().Get("title")
	if title == "" {
		title = tr(lang, "Выберите файл")
	}
	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = tr(lang, "Все файлы (*.*)|*.*")
	}
	path, err := BrowseFile(title, filter)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"path": path})
}

func parsePort(s string) int {
	n, _ := strconv.Atoi(s)
	if n <= 0 {
		return defaultBasePort
	}
	return n
}

const defaultBasePort = 8080

// portOwner — база реестра (кроме excludeID), за которой уже закреплён порт.
//
// Одновременно работать на одном порту две базы всё равно не могут, а лаунчер
// узнаёт свой процесс по порту: соседка по номеру выглядит «работающей»
// (порт занят), остановить её нельзя (identity на порту принадлежит другой
// базе), и до этой проверки такая пара блокировала «Стоп всё» вместе с
// закрытием окна. Дешевле не дать создать конфликт, чем объяснять его потом.
func portOwner(bases []*Base, excludeID string, port int) *Base {
	for _, b := range bases {
		if b == nil || b.ID == excludeID {
			continue
		}
		if b.Port == port {
			return b
		}
	}
	return nil
}

// portConflictError — текст отказа с подсказкой свободного порта.
func portConflictError(lang string, owner *Base, bases []*Base) string {
	return formatPortConflictError(lang, owner.Port, owner.Name, freeRegistryPort(bases))
}

func storedPortConflictError(lang string, err error) (string, bool) {
	var conflict *BasePortConflictError
	if !errors.As(err, &conflict) {
		return "", false
	}
	return formatPortConflictError(lang, conflict.Port, conflict.OwnerName, conflict.SuggestedPort), true
}

func formatPortConflictError(lang string, port int, ownerName string, suggestedPort int) string {
	return fmt.Sprintf(tr(lang, "Порт %d уже закреплён за базой «%s»: две базы не могут работать на одном порту. Свободный порт: %d"),
		port, ownerName, suggestedPort)
}

// freeRegistryPort — первый номер порта, не занятый другой базой реестра.
// Форма новой базы предлагает его сразу, чтобы порт по умолчанию не оказался
// третьим подряд 8080.
func freeRegistryPort(bases []*Base) int {
	used := make(map[int]bool, len(bases))
	for _, b := range bases {
		if b != nil {
			used[b.Port] = true
		}
	}
	for port := defaultBasePort; port < defaultBasePort+1000; port++ {
		if !used[port] {
			return port
		}
	}
	return defaultBasePort
}
