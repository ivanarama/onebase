package launcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// metadataApplySubdirs — подкаталоги, в которые ai-apply разрешает запись
// (метаданные, генерируемые этапом 2a; как в kindSubdir).
var metadataApplySubdirs = map[string]bool{
	"catalogs": true, "documents": true, "registers": true,
	"inforegs": true, "enums": true, "accounts": true, "accountregs": true,
}

// validApplyPath проверяет, что rel — это <метаданные-подкаталог>/<безопасное>.yaml.
// Возвращает нормализованный относительный путь (slash) или ошибку.
func validApplyPath(rel string) (string, error) {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	parts := strings.Split(rel, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("недопустимый путь: %q", rel)
	}
	if !metadataApplySubdirs[parts[0]] {
		return "", fmt.Errorf("недопустимый подкаталог: %q", parts[0])
	}
	if !strings.HasSuffix(parts[1], ".yaml") {
		return "", fmt.Errorf("ожидался .yaml: %q", rel)
	}
	name := strings.TrimSuffix(parts[1], ".yaml")
	if _, err := safeFileName(name); err != nil {
		return "", err
	}
	return parts[0] + "/" + parts[1], nil
}

// applyChanges записывает изменения в файловую конфигурацию baseDir. Возвращает
// число применённых и первую ошибку. Пути валидируются; запись только внутрь baseDir.
func applyChanges(baseDir string, changes []GenChange) (int, error) {
	if strings.TrimSpace(baseDir) == "" {
		return 0, fmt.Errorf("пустой путь конфигурации")
	}
	applied := 0
	for _, ch := range changes {
		rel, err := validApplyPath(ch.Path)
		if err != nil {
			return applied, err
		}
		full := filepath.Join(baseDir, filepath.FromSlash(rel))
		cleanBase := filepath.Clean(baseDir)
		if !strings.HasPrefix(filepath.Clean(full), cleanBase+string(os.PathSeparator)) {
			return applied, fmt.Errorf("путь вне базы: %q", rel)
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return applied, err
		}
		if err := os.WriteFile(full, []byte(ch.NewContent), 0o644); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

// cfgAIApply применяет предложенный diff (из ai-generate) в файловую конфигурацию.
// БД-базы пока не поддерживаются.
func (h *handler) cfgAIApply(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	if b.ConfigSource == "database" {
		writeJSON(w, 200, map[string]any{"error": "Применение в БД-базы пока не поддерживается — используйте файловую базу."})
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
		writeJSON(w, 400, map[string]any{"error": "Нет изменений для применения"})
		return
	}
	applied, err := applyChanges(b.Path, req.Changes)
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": err.Error(), "applied": applied})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "applied": applied})
}
