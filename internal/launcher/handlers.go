package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

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
		Running bool
		BaseURL string
	}

	selID := r.URL.Query().Get("sel")
	var selected *baseVM
	vms := make([]*baseVM, 0, len(bases))
	for _, b := range bases {
		vm := &baseVM{Base: b, Running: h.runner.IsRunning(b.ID), BaseURL: h.runner.BaseURL(b)}
		vms = append(vms, vm)
		if b.ID == selID {
			selected = vm
		}
	}
	if selected == nil && len(vms) > 0 {
		selected = vms[0]
	}

	render(w, "page-index", map[string]any{
		"Title":    "onebase — Информационные базы",
		"Bases":    vms,
		"Selected": selected,
		"BaseURL":  func() string {
			if selected != nil {
				return h.runner.BaseURL(selected.Base)
			}
			return ""
		}(),
	})
}

func (h *handler) newForm(w http.ResponseWriter, r *http.Request) {
	render(w, "page-form", map[string]any{
		"Title":  "onebase — Добавить базу",
		"IsNew":  true,
		"Base":   &Base{ConfigSource: "database", Port: 8080},
		"Error":  "",
	})
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	b := &Base{
		Name:         r.FormValue("name"),
		ConfigSource: r.FormValue("config_source"),
		Path:         r.FormValue("path"),
		DB:           r.FormValue("db"),
		Port:         parsePort(r.FormValue("port")),
	}

	if b.Name == "" || b.DB == "" {
		render(w, "page-form", map[string]any{
			"Title": "onebase — Добавить базу",
			"IsNew": true, "Base": b, "Error": "Наименование и строка подключения обязательны",
		})
		return
	}

	scaffold := r.FormValue("scaffold") == "1"

	if b.ConfigSource == "database" {
		if err := h.initDatabaseBase(r.Context(), b, scaffold); err != nil {
			render(w, "page-form", map[string]any{
				"Title": "onebase — Добавить базу",
				"IsNew": true, "Base": b, "Error": err.Error(),
			})
			return
		}
	} else {
		// file mode
		if b.Path == "" {
			render(w, "page-form", map[string]any{
				"Title": "onebase — Добавить базу",
				"IsNew": true, "Base": b, "Error": "Укажите путь к папке конфигурации",
			})
			return
		}
		if scaffold {
			if err := os.MkdirAll(b.Path, 0o755); err != nil {
				render(w, "page-form", map[string]any{
					"Title": "onebase — Добавить базу",
					"IsNew": true, "Base": b, "Error": "Не удалось создать папку: " + err.Error(),
				})
				return
			}
			if err := project.Scaffold(b.Path, b.Name); err != nil {
				render(w, "page-form", map[string]any{
					"Title": "onebase — Добавить базу",
					"IsNew": true, "Base": b, "Error": "Ошибка создания конфигурации: " + err.Error(),
				})
				return
			}
		}
		if err := storage.EnsureDatabase(r.Context(), b.DB); err != nil {
			render(w, "page-form", map[string]any{
				"Title": "onebase — Добавить базу",
				"IsNew": true, "Base": b, "Error": "Не удалось создать БД: " + err.Error(),
			})
			return
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
	render(w, "page-form", map[string]any{
		"Title": "onebase — Изменить базу",
		"IsNew": false, "Base": b, "Error": "",
	})
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.ParseForm()
	b.Name = r.FormValue("name")
	b.ConfigSource = r.FormValue("config_source")
	b.Path = r.FormValue("path")
	b.DB = r.FormValue("db")
	b.Port = parsePort(r.FormValue("port"))

	if b.Name == "" || b.DB == "" {
		render(w, "page-form", map[string]any{
			"Title": "onebase — Изменить базу",
			"IsNew": false, "Base": b, "Error": "Наименование и строка подключения обязательны",
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

func (h *handler) start(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}

	if !h.runner.IsRunning(b.ID) {
		if err := storage.EnsureDatabase(r.Context(), b.DB); err != nil {
			writeJSON(w, 500, map[string]any{"error": "Не удалось создать БД: " + err.Error()})
			return
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

func (h *handler) migrate(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	out, runErr := h.runner.MigrateBase(r.Context(), b)
	errMsg := ""
	if runErr != nil {
		errMsg = runErr.Error()
	}
	render(w, "page-migrate", map[string]any{
		"Title":  "onebase — Обновление БД",
		"Name":   b.Name,
		"Output": out,
		"Error":  errMsg,
	})
}

func (h *handler) configExport(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if b.ConfigSource != "database" {
		render(w, "page-config-result", map[string]any{
			"Title":   "onebase — Конфигуратор",
			"Message": "Выгрузка доступна только для баз в режиме «В базе данных».",
			"Error":   "",
		})
		return
	}

	db, err := storage.Connect(r.Context(), b.DB)
	if err != nil {
		render(w, "page-config-result", map[string]any{
			"Title": "onebase — Конфигуратор", "Message": "",
			"Error": "Ошибка подключения: " + err.Error(),
		})
		return
	}
	defer db.Close()

	workDir, err := workspacePath(b.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	repo := configdb.New(db.Pool())
	if err := repo.ExportToDir(r.Context(), workDir); err != nil {
		render(w, "page-config-result", map[string]any{
			"Title": "onebase — Конфигуратор", "Message": "",
			"Error": "Ошибка выгрузки: " + err.Error(),
		})
		return
	}

	OpenPath(workDir)

	render(w, "page-config-result", map[string]any{
		"Title":   "onebase — Конфигуратор",
		"Message": fmt.Sprintf("Конфигурация выгружена в папку: %s", workDir),
		"Error":   "",
	})
}

func (h *handler) configImport(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	r.ParseForm()
	srcDir := r.FormValue("path")
	if srcDir == "" {
		srcDir, _ = workspacePath(b.ID)
	}

	db, err := storage.Connect(r.Context(), b.DB)
	if err != nil {
		render(w, "page-config-result", map[string]any{
			"Title": "onebase — Загрузка конфигурации", "Message": "",
			"Error": "Ошибка подключения: " + err.Error(),
		})
		return
	}
	defer db.Close()

	repo := configdb.New(db.Pool())
	if err := repo.ImportFromDir(r.Context(), srcDir); err != nil {
		render(w, "page-config-result", map[string]any{
			"Title": "onebase — Загрузка конфигурации", "Message": "",
			"Error": "Ошибка загрузки: " + err.Error(),
		})
		return
	}

	// Migrate after import
	out, _ := h.runner.MigrateBase(r.Context(), b)
	render(w, "page-config-result", map[string]any{
		"Title":   "onebase — Загрузка конфигурации",
		"Message": fmt.Sprintf("Конфигурация загружена из: %s\n\nМиграция:\n%s", srcDir, out),
		"Error":   "",
	})
}

func (h *handler) initDatabaseBase(ctx context.Context, b *Base, scaffold bool) error {
	if err := storage.EnsureDatabase(ctx, b.DB); err != nil {
		return fmt.Errorf("создание БД: %w", err)
	}
	db, err := storage.Connect(ctx, b.DB)
	if err != nil {
		return fmt.Errorf("подключение к БД: %w", err)
	}
	defer db.Close()

	repo := configdb.New(db.Pool())
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

func render(w http.ResponseWriter, name string, data map[string]any) {
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

func parsePort(s string) int {
	n, _ := strconv.Atoi(s)
	if n <= 0 {
		return 8080
	}
	return n
}

