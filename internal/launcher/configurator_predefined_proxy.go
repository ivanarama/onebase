package launcher

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/fsmode"
	"gopkg.in/yaml.v3"
)

func (h *handler) configuratorSavePredefined(w http.ResponseWriter, r *http.Request) {
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
	entityName := r.FormValue("entity")
	fieldNames := r.Form["pre_field_names"]
	var predefined []rawPredefinedRow
	for _, i := range formRowIndices(r, "pre") {
		name := strings.TrimSpace(r.FormValue(fmt.Sprintf("pre.%d.name", i)))
		if name == "" {
			continue // строку очистили — считаем удалённой
		}
		fields := make(map[string]interface{})
		for _, fn := range fieldNames {
			if v := r.FormValue(fmt.Sprintf("pre.%d.field.%s", i, fn)); v != "" {
				fields[fn] = v
			}
		}
		pd := rawPredefinedRow{Name: name}
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

func savePredefinedToFile(dir, entityName string, predefined []rawPredefinedRow) error {
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
			out, err := applyPredefined(raw, subdir+"/"+e.Name(), predefined)
			if err != nil {
				return err
			}
			// p собран из каталога базы и имени файла, полученного от os.ReadDir
			// того же каталога: ReadDir отдаёт базовые имена, выйти за пределы
			// dir/subdir нечем. gosec этого не распознаёт.
			return os.WriteFile(p, out, fsmode.File) //nolint:gosec // G703: путь построен из ReadDir-имени, traversal невозможен
		}
	}
	return fmt.Errorf("entity %q not found", entityName)
}

func (h *handler) savePredefinedToDB(ctx context.Context, b *Base, entityName string, predefined []rawPredefinedRow) error {
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
	out, err := applyPredefined(rawContent, targetPath, predefined)
	if err != nil {
		return err
	}
	return cfgUpsert(ctx, db, targetPath, out)
}

// rawPredefinedRow — одна строка формы «Предопределённые элементы».
type rawPredefinedRow struct {
	Name   string                 `yaml:"name"`
	Fields map[string]interface{} `yaml:"fields,omitempty"`
}

// applyPredefined точечно заменяет блок predefined в YAML сущности, сохраняя
// порядок остальных ключей и комментарии. Пустой список удаляет ключ.
//
// Раньше файл круглился через map[string]interface{}: порядок ключей и
// комментарии терялись при каждом сохранении — тот же класс, что #656 у
// app.yaml. Идиома взята у applyAccountRegFields.
func applyPredefined(raw []byte, what string, predefined []rawPredefinedRow) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("applyPredefined: ожидалось YAML-отображение в корне %s", what)
	}
	// Пустой список — ключ убрать. Типизированный nil-слайс, обёрнутый в any,
	// не равен nil, поэтому передаём нетипизированный (ср. anyOrNil).
	var val any
	if len(predefined) > 0 {
		val = predefined
	}
	if err := setYAMLMapField(root.Content[0], "predefined", val); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2) // файл пользовательский и лежит под git — не менять отступ
	if err := enc.Encode(&root); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
	cookie, err := r.Cookie(configuratorSessionCookieName)
	if err != nil || cookie.Value == "" {
		// Нет сессии пользовательского режима — клиент откроет /ui без bootstrap.
		writeJSON(w, 200, map[string]string{"code": ""})
		return
	}
	client, cleanup, err := singleConnectionControlClient(b.Port, 5*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "UI server unreachable: " + err.Error()})
		return
	}
	defer cleanup()
	expectedPID, _ := h.runner.trackedProcessPID(b.ID)
	if _, err := controlProcessIdentityWithClient(b, expectedPID, client); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "Не удалось безопасно подтвердить процесс базы. Перезапустите базу и повторите попытку.",
		})
		return
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/auth/one-time-code", b.Port)
	// Адрес не пользовательский: схема и хост фиксированы, порт берётся
	// из реестра баз лаунчера. Внешний URL сюда подставить нельзя.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, nil) //nolint:gosec // G704: цель — localhost:<порт базы> из реестра
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	req.AddCookie(&http.Cookie{Name: "onebase_session", Value: cookie.Value})

	resp, err := client.Do(req) //nolint:gosec // G704: fixed authenticated localhost connection
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "UI server unreachable: " + err.Error()})
		return
	}
	defer closeRead("ответ сервера базы", resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	// Ответ уже начат — статус не поменять; обрыв фиксируем и прекращаем.
	copyProxied(w, resp.Body)
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

	// Внутренний токен — процесс базы примет debug-запрос только с ним.
	tok := h.runner.DebugToken(baseID)
	if tok == "" {
		// Debug bearer process-local и намеренно не хранится: отправка
		// persistent control secret процессу лишь по номеру порта раскрывала бы
		// его чужому listener. После рестарта launcher базу надо перезапустить.
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "Перезапустите базу из текущего лаунчера, чтобы открыть отладчик",
		})
		return
	}
	client, cleanup, err := singleConnectionControlClient(b.Port, controlProbeTimeout)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "UI server unreachable: " + err.Error()})
		return
	}
	defer cleanup()
	expectedPID, tracked := h.runner.trackedProcessPID(baseID)
	if !tracked {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Перезапустите базу из текущего лаунчера, чтобы открыть отладчик"})
		return
	}
	if _, err := controlProcessIdentityWithClient(b, expectedPID, client); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Процесс базы изменился; перезапустите базу и повторите попытку"})
		return
	}
	client.Timeout = 0 // evaluate/profile requests may legitimately be long-running

	uiURL := fmt.Sprintf("http://127.0.0.1:%d/debug/global/%s", b.Port, action)
	req, err := http.NewRequestWithContext(r.Context(), r.Method, uiURL, r.Body) //nolint:gosec // G704: authenticated fixed localhost connection
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("X-OneBase-Debug-Token", tok)

	resp, err := client.Do(req) //nolint:gosec // G704: fixed authenticated localhost connection
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "UI server unreachable: " + err.Error()})
		return
	}
	defer closeRead("ответ сервера базы", resp.Body)

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	// Ответ уже начат — статус не поменять; обрыв фиксируем и прекращаем.
	copyProxied(w, resp.Body)
}
