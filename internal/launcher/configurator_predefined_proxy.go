package launcher

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

func (h *handler) configuratorSavePredefined(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	entityName := r.FormValue("entity")
	// collect predefined items
	type rawPD struct {
		Name   string                 `yaml:"name"`
		Fields map[string]interface{} `yaml:"fields,omitempty"`
	}
	var predefined []rawPD
	fieldNames := r.Form["pre_field_names"]
	for i := 0; i < 500; i++ {
		name := strings.TrimSpace(r.FormValue(fmt.Sprintf("pre.%d.name", i)))
		if name == "" {
			break
		}
		fields := make(map[string]interface{})
		for _, fn := range fieldNames {
			if v := r.FormValue(fmt.Sprintf("pre.%d.field.%s", i, fn)); v != "" {
				fields[fn] = v
			}
		}
		pd := rawPD{Name: name}
		if len(fields) > 0 {
			pd.Fields = fields
		}
		predefined = append(predefined, pd)
	}

	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.savePredefinedToDB(r.Context(), b, entityName, predefined)
	} else {
		saveErr = savePredefinedToFile(b.Path, entityName, predefined)
	}
	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = entityName
	}
	renderCfg(w, r, data)
}

func savePredefinedToFile(dir, entityName string, predefined interface{}) error {
	// find entity file in catalogs/ or documents/
	for _, subdir := range []string{"catalogs", "documents"} {
		entries, _ := os.ReadDir(filepath.Join(dir, subdir))
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			p := filepath.Join(dir, subdir, e.Name())
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var top struct {
				Name string `yaml:"name"`
			}
			if yaml.Unmarshal(raw, &top) != nil || top.Name != entityName {
				continue
			}
			var node map[string]interface{}
			if err := yaml.Unmarshal(raw, &node); err != nil {
				return err
			}
			if predefined == nil {
				delete(node, "predefined")
			} else {
				node["predefined"] = predefined
			}
			out, err := yaml.Marshal(node)
			if err != nil {
				return err
			}
			return os.WriteFile(p, out, 0o644)
		}
	}
	return fmt.Errorf("entity %q not found", entityName)
}

func (h *handler) savePredefinedToDB(ctx context.Context, b *Base, entityName string, predefined interface{}) error {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	rows, err := db.Query(ctx, `SELECT path, content FROM _onebase_config WHERE path ~ '^(catalogs|documents)/[^/]+\.yaml$'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var targetPath string
	var rawContent []byte
	for rows.Next() {
		var p string
		var content []byte
		if err := rows.Scan(&p, &content); err != nil {
			continue
		}
		var top struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(content, &top) == nil && top.Name == entityName {
			targetPath = p
			rawContent = content
			break
		}
	}
	rows.Close()
	if targetPath == "" {
		return fmt.Errorf("entity %q not found in DB config", entityName)
	}
	var node map[string]interface{}
	if err := yaml.Unmarshal(rawContent, &node); err != nil {
		return err
	}
	if predefined == nil {
		delete(node, "predefined")
	} else {
		node["predefined"] = predefined
	}
	out, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	return cfgUpsert(ctx, db, targetPath, out)
}

// ── one-time code proxy ──────────────────────────────────────────────────────

// oneTimeCodeProxy запрашивает у процесса базы одноразовый bootstrap-код для
// текущей сессии (план 53): конфигуратор больше не вшивает сессионный токен в
// URL пользовательского режима (?_tk=) — JS дёргает этот эндпоинт (same-origin,
// без CORS) и открывает /auth/bootstrap?code=<одноразовый>.
func (h *handler) oneTimeCodeProxy(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "base not found"})
		return
	}
	authorized, authErr := h.cfgAdminAuthorized(r, b)
	if authErr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Сервис аутентификации недоступен"})
		return
	}
	if !authorized {
		writeJSON(w, 401, map[string]string{"error": "Требуется вход администратора"})
		return
	}
	cookie, err := r.Cookie("onebase_session")
	if err != nil || cookie.Value == "" {
		// Нет сессии пользовательского режима — клиент откроет /ui без bootstrap.
		writeJSON(w, 200, map[string]string{"code": ""})
		return
	}

	url := fmt.Sprintf("http://localhost:%d/auth/one-time-code", b.Port)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, nil)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	req.AddCookie(&http.Cookie{Name: "onebase_session", Value: cookie.Value})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "UI server unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ── debug proxy ──────────────────────────────────────────────────────────────

// debugProxy forwards debug API requests from the configurator (launcher server)
// to the UI server, avoiding CORS issues in the webview.
func (h *handler) debugProxy(w http.ResponseWriter, r *http.Request) {
	baseID := chi.URLParam(r, "id")
	action := chi.URLParam(r, "action")

	b, err := h.store.Get(baseID)
	if err != nil {
		http.Error(w, "base not found", 404)
		return
	}

	// Требуем сессию админа конфигуратора. 401 JSON (не 302), т.к. это API для JS.
	authorized, authErr := h.cfgAdminAuthorized(r, b)
	if authErr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Сервис аутентификации недоступен"})
		return
	}
	if !authorized {
		writeJSON(w, 401, map[string]string{"error": "Требуется вход администратора"})
		return
	}

	uiURL := fmt.Sprintf("http://localhost:%d/debug/global/%s", b.Port, action)

	req, err := http.NewRequestWithContext(r.Context(), r.Method, uiURL, r.Body)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Forward Content-Type from original request
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	// Внутренний токен — процесс базы примет debug-запрос только с ним.
	if tok := h.runner.DebugToken(baseID); tok != "" {
		req.Header.Set("X-OneBase-Debug-Token", tok)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "UI server unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
