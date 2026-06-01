package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"gopkg.in/yaml.v3"
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
		if st, err := os.Stat(p); err == nil && st.IsDir() {
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
	store  *Store
	runner *Runner
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	bases, err := h.store.List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	type baseVM struct {
		*Base
		Running    bool
		BaseURL    string
		AppName    string
		AppVersion string
		LogoBase64 string
	}

	loadAppInfo := func(b *Base, vm *baseVM) {
		var cfg struct {
			Name    string `yaml:"name"`
			Version string `yaml:"version"`
			Logo    string `yaml:"logo"`
		}
		if b.ConfigSource == "database" {
			db, err := OpenDB(context.Background(), b)
			if err != nil {
				return
			}
			defer db.Close()
			var content []byte
			if err := db.QueryRow(context.Background(), "SELECT content FROM _onebase_config WHERE path = $1", "config/app.yaml").Scan(&content); err != nil {
				return
			}
			yaml.Unmarshal(content, &cfg)
		} else if b.Path != "" {
			data, err := os.ReadFile(filepath.Join(b.Path, "config", "app.yaml"))
			if err != nil {
				return
			}
			yaml.Unmarshal(data, &cfg)
		}
		vm.AppName = cfg.Name
		vm.AppVersion = cfg.Version
		if cfg.Logo != "" {
			vm.LogoBase64 = "/bases/" + b.ID + "/configurator/logo"
		}
	}

	selID := r.URL.Query().Get("sel")
	var selected *baseVM
	vms := make([]*baseVM, 0, len(bases))
	for _, b := range bases {
		vm := &baseVM{Base: b, Running: h.runner.IsRunning(b.ID), BaseURL: h.runner.BaseURL(b)}
		loadAppInfo(b, vm)
		vms = append(vms, vm)
		if b.ID == selID {
			selected = vm
		}
	}
	if selected == nil && len(vms) > 0 {
		selected = vms[0]
	}

	render(w, r, "page-index", map[string]any{
		"Title":    tr(resolveLang(r), "onebase — Информационные базы"),
		"Bases":    vms,
		"Selected": selected,
		"BaseURL": func() string {
			if selected != nil {
				return h.runner.BaseURL(selected.Base)
			}
			return ""
		}(),
	})
}

func (h *handler) newForm(w http.ResponseWriter, r *http.Request) {
	render(w, r, "page-form", map[string]any{
		"Title": tr(resolveLang(r), "onebase — Добавить базу"),
		"IsNew": true,
		"Base":  &Base{ConfigSource: "file", DBType: "sqlite", Port: 8080},
		"Error": "",
	})
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
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

	scaffold := r.FormValue("scaffold") == "1"

	if b.ConfigSource == "database" {
		if err := h.initDatabaseBase(r.Context(), b, scaffold); err != nil {
			render(w, r, "page-form", map[string]any{
				"Title": tr(lang, "onebase — Добавить базу"),
				"IsNew": true, "Base": b, "Error": err.Error(),
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
			if err := os.MkdirAll(b.Path, 0o755); err != nil {
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
	r.ParseForm()
	b.Name = r.FormValue("name")
	b.ConfigSource = r.FormValue("config_source")
	b.Path = r.FormValue("path")
	b.DB = r.FormValue("db")
	if dt := r.FormValue("db_type"); dt != "" {
		b.DBType = dt
	}
	b.DBPath = r.FormValue("db_path")
	b.Port = parsePort(r.FormValue("port"))

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
	if err := h.store.Update(b); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/?sel="+b.ID, http.StatusFound)
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.runner.Stop(id)
	h.store.Remove(id)
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

func (h *handler) start(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	lang := resolveLang(r)

	if !h.runner.IsRunning(b.ID) {
		if b.DBType != "sqlite" {
			if err := storage.EnsureDatabase(r.Context(), b.DB); err != nil {
				writeJSON(w, 500, map[string]any{"error": tr(lang, "Не удалось создать БД") + ": " + err.Error()})
				return
			}
		}
		if err := h.runner.Start(b); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		b.LastOpened = time.Now()
		h.store.Update(b)
	}

	// Wait until the base server is ready before handing the URL to the browser
	if err := h.runner.WaitReady(b, 15*time.Second); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, 200, map[string]any{"url": h.runner.BaseURL(b)})
}

func (h *handler) stop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.runner.Stop(id)
	http.Redirect(w, r, "/?sel="+id, http.StatusFound)
}

func (h *handler) killAll(w http.ResponseWriter, r *http.Request) {
	sel := r.URL.Query().Get("sel")

	// Collect all known base ports so we can kill processes even if not tracked.
	var ports []int
	if bases, err := h.store.List(); err == nil {
		for _, b := range bases {
			ports = append(ports, b.Port)
		}
	}
	h.runner.StopAll(ports)

	redirect := "/"
	if sel != "" {
		redirect = "/?sel=" + sel
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
		json.NewEncoder(w).Encode(map[string]any{"output": out, "error": runErr.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"output": out, "error": ""})
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
	if err := r.ParseMultipartForm(32 << 20); err != nil && err != http.ErrNotMultipart {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
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
		"running":       h.runner.IsRunning(b.ID),
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
	if b.DBType != "sqlite" {
		if err := storage.EnsureDatabase(r.Context(), b.DB); err != nil {
			writeJSON(w, 500, map[string]any{"error": tr(lang, "Не удалось создать БД") + ": " + err.Error()})
			return
		}
	}
	if err := h.runner.Restart(b); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	b.LastOpened = time.Now()
	h.store.Update(b)
	if err := h.runner.WaitReady(b, 15*time.Second); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
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
	if b.ConfigSource != "database" {
		render(w, r, "page-config-result", map[string]any{
			"Title":   tr(lang, "onebase — Конфигуратор"),
			"Message": tr(lang, "Выгрузка доступна только для баз в режиме «В базе данных»."),
			"Error":   "",
		})
		return
	}

	db, err := OpenDB(r.Context(), b)
	if err != nil {
		render(w, r, "page-config-result", map[string]any{
			"Title": tr(lang, "onebase — Конфигуратор"), "Message": "",
			"Error": tr(lang, "Ошибка подключения") + ": " + err.Error(),
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
			"Error": tr(lang, "Ошибка выгрузки") + ": " + err.Error(),
		})
		return
	}

	OpenPath(workDir)

	render(w, r, "page-config-result", map[string]any{
		"Title":   tr(lang, "onebase — Конфигуратор"),
		"Message": fmt.Sprintf(tr(lang, "Конфигурация выгружена в папку")+": %s", workDir),
		"Error":   "",
	})
}

func (h *handler) configImport(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)

	r.ParseForm()
	srcDir := r.FormValue("path")
	if srcDir == "" {
		srcDir, _ = workspacePath(b.ID)
	}

	db, err := OpenDB(r.Context(), b)
	if err != nil {
		render(w, r, "page-config-result", map[string]any{
			"Title": tr(lang, "onebase — Загрузка конфигурации"), "Message": "",
			"Error": tr(lang, "Ошибка подключения") + ": " + err.Error(),
		})
		return
	}
	defer db.Close()

	repo := configdb.New(db)
	if err := repo.ImportFromDir(r.Context(), srcDir); err != nil {
		render(w, r, "page-config-result", map[string]any{
			"Title": tr(lang, "onebase — Загрузка конфигурации"), "Message": "",
			"Error": tr(lang, "Ошибка загрузки") + ": " + err.Error(),
		})
		return
	}

	// Migrate after import
	out, _ := h.runner.MigrateBase(r.Context(), b)
	render(w, r, "page-config-result", map[string]any{
		"Title":   tr(lang, "onebase — Загрузка конфигурации"),
		"Message": fmt.Sprintf(tr(lang, "Конфигурация загружена из")+": %s\n\n"+tr(lang, "Миграция")+":\n%s", srcDir, out),
		"Error":   "",
	})
}

func (h *handler) initDatabaseBase(ctx context.Context, b *Base, scaffold bool) error {
	if b.DBType != "sqlite" {
		if err := storage.EnsureDatabase(ctx, b.DB); err != nil {
			return fmt.Errorf("создание БД: %w", err)
		}
	}
	db, err := OpenDB(ctx, b)
	if err != nil {
		return fmt.Errorf("подключение к БД: %w", err)
	}
	defer db.Close()

	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("создание схемы configdb: %w", err)
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
		defer os.RemoveAll(tmpDir)

		if err := project.Scaffold(tmpDir, name); err != nil {
			return fmt.Errorf("создание конфигурации: %w", err)
		}
		if err := repo.ImportFromDir(ctx, tmpDir); err != nil {
			return fmt.Errorf("загрузка конфигурации: %w", err)
		}
	}
	return nil
}

func workspacePath(baseID string) (string, error) {
	p, err := OnebasePath("workspace", baseID)
	if err != nil {
		return "", err
	}
	return p, os.MkdirAll(p, 0o755)
}

func resolveLang(r *http.Request) string {
	if launcherBundle != nil {
		return i18n.Resolve("", "ru", r.Header.Get("Accept-Language"), launcherBundle)
	}
	return "ru"
}

func tr(lang, key string) string {
	if launcherBundle != nil {
		return launcherBundle.T(lang, key)
	}
	return key
}

func render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	if _, ok := data["Lang"]; !ok {
		data["Lang"] = resolveLang(r)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
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
		return 8080
	}
	return n
}
