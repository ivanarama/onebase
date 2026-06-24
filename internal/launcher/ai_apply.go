package launcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
)

// applyableSubdirs — однофайловые YAML-подкаталоги конфигурации, куда разрешено
// применять AI-generated changes. Более сложные семейства (src/forms/config)
// проверяются отдельными правилами в safeConfigPath.
var applyableSubdirs = map[string]bool{
	"catalogs":    true,
	"documents":   true,
	"registers":   true,
	"inforegs":    true,
	"enums":       true,
	"constants":   true,
	"accounts":    true,
	"accountregs": true,
	"reports":     true,
	"processors":  true,
	"widgets":     true,
	"pages":       true,
	"services":    true,
	"subsystems":  true,
	"roles":       true,
	"scheduled":   true,
	"journals":    true,
}

// winReservedNames — зарезервированные имена устройств Windows (без расширения,
// регистронезависимо). Файл с таким именем нельзя надёжно создать на Windows.
var winReservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// safeConfigPath проверяет относительный slash-путь объекта каркаса перед
// записью в реальную конфигурацию: путь из белого списка, без обхода каталогов
// и без проблемных для Windows имён.
func safeConfigPath(rel string) error {
	if strings.TrimSpace(rel) == "" {
		return fmt.Errorf("пустой путь")
	}
	if rel != path.Clean(rel) || path.IsAbs(rel) || strings.Contains(rel, "..") ||
		strings.ContainsRune(rel, '\\') || strings.ContainsRune(rel, 0) {
		return fmt.Errorf("недопустимый путь: %q", rel)
	}
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		if part == "" || strings.ContainsAny(part, `:*?"<>|`) {
			return fmt.Errorf("недопустимый сегмент пути: %q", part)
		}
		stem := strings.ToLower(strings.TrimSuffix(part, path.Ext(part)))
		if winReservedNames[stem] {
			return fmt.Errorf("зарезервированное имя файла: %q", part)
		}
	}
	subdir := parts[0]
	fname := parts[len(parts)-1]
	switch {
	case applyableSubdirs[subdir]:
		if len(parts) != 2 {
			return fmt.Errorf("ожидался путь вида «%s/имя.yaml»: %q", subdir, rel)
		}
		if !strings.HasSuffix(fname, ".yaml") {
			return fmt.Errorf("ожидался .yaml-файл: %q", fname)
		}
	case subdir == "src":
		if len(parts) != 2 || !strings.HasSuffix(fname, ".os") {
			return fmt.Errorf("ожидался путь вида «src/имя.os»: %q", rel)
		}
	case subdir == "forms":
		if len(parts) < 2 || len(parts) > 3 ||
			!(strings.HasSuffix(fname, ".form.yaml") || strings.HasSuffix(fname, ".form.os")) {
			return fmt.Errorf("ожидался путь forms/*.form.yaml, forms/*/*.form.yaml или .form.os: %q", rel)
		}
	case subdir == "config":
		if len(parts) != 2 || (fname != "app.yaml" && fname != "home_page.yaml") {
			return fmt.Errorf("разрешены только config/app.yaml и config/home_page.yaml: %q", rel)
		}
	default:
		return fmt.Errorf("недопустимый подкаталог: %q", subdir)
	}
	return nil
}

// cfgAIApply применяет сгенерированный каркас (changes из cfgAIGenerate) в
// конфигурацию базы: проверяет каждый путь и записывает объект в нужный режим
// хранения. Новые объекты появятся в схеме данных только после миграции базы.
func (h *handler) cfgAIApply(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		Changes []GenChange `json:"changes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "Некорректный запрос"})
		return
	}
	if len(req.Changes) == 0 {
		writeJSON(w, 200, map[string]any{"error": "Нет изменений для применения"})
		return
	}
	// Сначала проверяем все пути — чтобы небезопасный путь не оставил
	// частично применённого каркаса.
	for _, ch := range req.Changes {
		if err := safeConfigPath(ch.Path); err != nil {
			writeJSON(w, 200, map[string]any{"error": "недопустимый путь " + ch.Path + ": " + err.Error()})
			return
		}
	}
	files := make([]configFileEntry, 0, len(req.Changes))
	for _, ch := range req.Changes {
		files = append(files, configFileEntry{relPath: ch.Path, content: []byte(ch.NewContent)})
	}
	if err := saveConfigFilesWithVersion(r, h, b, files, configdb.VersionOptions{
		AuthorLogin: cfgLogin(r.Context()),
		Message:     fmt.Sprintf("ai apply %d files", len(files)),
	}); err != nil {
		writeJSON(w, 200, map[string]any{"error": "не удалось применить изменения: " + err.Error(), "applied": 0})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "applied": len(req.Changes)})
}
