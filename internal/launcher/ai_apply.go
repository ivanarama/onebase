package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/ivantit66/onebase/internal/configdb"
)

// applyableSubdirs — подкаталоги метаданных, куда разрешено применять
// сгенерированный каркас. Совпадает с целевыми подкаталогами kindSubdir
// (ai_generate.go): на этапе генерации каркаса создаются только метаданные.
var applyableSubdirs = map[string]bool{
	"catalogs":    true,
	"documents":   true,
	"registers":   true,
	"inforegs":    true,
	"enums":       true,
	"accounts":    true,
	"accountregs": true,
	"reports":     true,
	"widgets":     true,
	"journals":    true,
	"processors":  true,
	"pages":       true,
	"subsystems":  true,
	"roles":       true,
	"services":    true,
	"scheduled":   true,
	"constants":   true,
	"printforms":  true,
	"forms":       true,
	"src":         true,
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
// записью в реальную конфигурацию: ровно «подкаталог/имя.yaml», подкаталог из
// белого списка, без обхода каталогов и без проблемных для Windows имён.
func safeConfigPath(rel string) error {
	_, err := safeGeneratedRelPath(rel)
	return err
}

func safeGeneratedRelPath(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("пустой путь")
	}
	rel = path.Clean(rel)
	if rel == "." || strings.Contains(rel, "..") ||
		strings.ContainsRune(rel, '\\') || strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("недопустимый путь: %q", rel)
	}
	parts := strings.Split(rel, "/")
	if len(parts) < 2 || parts[0] == "" || parts[len(parts)-1] == "" {
		return "", fmt.Errorf("ожидался относительный путь внутри подкаталога конфигурации: %q", rel)
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("недопустимый сегмент пути: %q", rel)
		}
	}
	subdir, fname := parts[0], parts[len(parts)-1]
	if !applyableSubdirs[subdir] {
		return "", fmt.Errorf("недопустимый подкаталог: %q", subdir)
	}
	if subdir == "forms" {
		if len(parts) < 3 {
			return "", fmt.Errorf("для forms ожидается путь вида forms/<объект>/<файл>: %q", rel)
		}
	} else if len(parts) != 2 {
		return "", fmt.Errorf("для %s ожидается плоский путь вида %s/<файл>: %q", subdir, subdir, rel)
	}
	low := strings.ToLower(fname)
	switch subdir {
	case "src":
		if !strings.HasSuffix(low, ".os") && !strings.HasSuffix(low, ".layout.yaml") {
			return "", fmt.Errorf("в src разрешены только .os и .layout.yaml: %q", fname)
		}
	case "forms":
		if !strings.HasSuffix(low, ".form.yaml") && !strings.HasSuffix(low, ".form.os") {
			return "", fmt.Errorf("в forms разрешены только .form.yaml и .form.os: %q", fname)
		}
	default:
		if !strings.HasSuffix(low, ".yaml") {
			return "", fmt.Errorf("ожидался .yaml-файл: %q", fname)
		}
	}
	if strings.ContainsAny(fname, `:*?"<>|`) {
		return "", fmt.Errorf("недопустимое имя файла: %q", fname)
	}
	stem := strings.ToLower(strings.TrimSuffix(fname, path.Ext(fname)))
	if winReservedNames[stem] {
		return "", fmt.Errorf("зарезервированное имя файла: %q", fname)
	}
	return rel, nil
}

func safeGeneratedFullPath(root, rel string) (string, error) {
	cleanRel, err := safeGeneratedRelPath(rel)
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.FromSlash(cleanRel))
	cleanRoot := filepath.Clean(root)
	if fullClean := filepath.Clean(full); fullClean != cleanRoot && !strings.HasPrefix(fullClean, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("путь вне overlay: %q", rel)
	}
	return full, nil
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
	dir, cleanup, err := materializeProject(r.Context(), h, b)
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": "не удалось подготовить проверку изменений: " + err.Error()})
		return
	}
	if cleanup != nil {
		defer cleanup()
	}
	baseCheck := configcheck.RunFull(dir)
	g, err := newGenSession(dir)
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": "не удалось создать staging для проверки: " + err.Error()})
		return
	}
	defer g.close()
	for _, ch := range req.Changes {
		if err := g.createFile(ch.Path, ch.NewContent); err != nil {
			writeJSON(w, 200, map[string]any{"error": "не удалось подготовить " + ch.Path + ": " + err.Error()})
			return
		}
	}
	check := configcheck.RunFull(g.overlay)
	if baseCheck.OK && !check.OK {
		writeJSON(w, 200, map[string]any{
			"error": "изменения не проходят onebase check; файлы не применены",
			"check": check,
		})
		return
	}
	var beforeVersion string
	if b.ConfigSource == "database" {
		v, err := h.latestConfigVersionID(r.Context(), b)
		if err != nil {
			writeJSON(w, 200, map[string]any{"error": "не удалось прочитать текущую версию конфигурации: " + err.Error()})
			return
		}
		beforeVersion = v
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
	var afterVersion string
	if b.ConfigSource == "database" {
		v, err := h.latestConfigVersionID(r.Context(), b)
		if err != nil {
			writeJSON(w, 200, map[string]any{"error": "файлы применены, но не удалось прочитать новую версию конфигурации: " + err.Error(), "applied": len(req.Changes), "beforeVersion": beforeVersion})
			return
		}
		afterVersion = v
	}
	writeJSON(w, 200, map[string]any{"ok": true, "applied": len(req.Changes), "check": check, "beforeVersion": beforeVersion, "afterVersion": afterVersion})
}

func (h *handler) latestConfigVersionID(ctx context.Context, b *Base) (string, error) {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return "", err
	}
	defer db.Close()
	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		return "", err
	}
	versions, err := repo.ListVersions(ctx, 1)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", nil
	}
	return versions[0].ID, nil
}
