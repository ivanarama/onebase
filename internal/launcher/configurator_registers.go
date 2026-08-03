package launcher

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

func findRegisterFilePath(dir, regName string) (string, error) {
	items, _ := os.ReadDir(filepath.Join(dir, "registers"))
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		p := filepath.Join(dir, "registers", item.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var hdr struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(data, &hdr) == nil && hdr.Name == regName {
			return p, nil
		}
	}
	return "", fmt.Errorf("register %q not found", regName)
}

func saveRegisterFieldsToFile(dir, regName string, dims, res, attrs []saveField, objTitles *map[string]string) error {
	filePath, err := findRegisterFilePath(dir, regName)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	var reg saveRegister
	if err := yaml.Unmarshal(raw, &reg); err != nil {
		return err
	}
	reg.Dimensions = dims
	reg.Resources = res
	reg.Attributes = attrs
	if objTitles != nil {
		reg.Titles = *objTitles
	}
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, out, 0o644)
}

func (h *handler) saveRegisterFieldsToDB(ctx context.Context, b *Base, regName string, dims, res, attrs []saveField, objTitles *map[string]string) error {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(ctx,
		`SELECT path, content FROM _onebase_config WHERE path LIKE 'registers/%.yaml'`)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var targetPath string
	var reg saveRegister
	for rows.Next() {
		var p string
		var content []byte
		if err := rows.Scan(&p, &content); err != nil {
			continue
		}
		var r saveRegister
		if yaml.Unmarshal(content, &r) == nil && r.Name == regName {
			targetPath = p
			reg = r
			break
		}
	}
	rows.Close()
	if targetPath == "" {
		return fmt.Errorf("register %q not found in DB config", regName)
	}

	reg.Dimensions = dims
	reg.Resources = res
	reg.Attributes = attrs
	if objTitles != nil {
		reg.Titles = *objTitles
	}
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	return cfgUpsert(ctx, db, targetPath, out)
}

func (h *handler) configuratorSaveRegisterFields(w http.ResponseWriter, r *http.Request) {
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
	regName := r.FormValue("register")

	dims := parseRegSection(r, "dim")
	res := parseRegSection(r, "res")
	attrs := parseRegSection(r, "attr")

	var regTitles *map[string]string
	if formHasMapField(r, "titles") {
		t := parseMapForm(r, "titles")
		regTitles = &t
	}

	if err := validateRegisterFieldEdit(regName, dims, res, attrs); err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка проверки") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.saveRegisterFieldsToDB(r.Context(), b, regName, dims, res, attrs, regTitles)
	} else {
		saveErr = saveRegisterFieldsToFile(b.Path, regName, dims, res, attrs, regTitles)
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = regName
	}
	renderCfg(w, r, data)
}

// ── New object creation ───────────────────────────────────────────────────────

func findInfoRegFilePath(dir, name string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "inforegs"))
	if err != nil {
		return "", fmt.Errorf("inforegs dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		p := filepath.Join(dir, "inforegs", e.Name())
		raw, _ := os.ReadFile(p)
		var tmp struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(raw, &tmp) == nil && tmp.Name == name {
			return p, nil
		}
	}
	return "", fmt.Errorf("inforeg %q not found", name)
}

func saveInfoRegToFile(dir string, reg saveInfoReg, objTitles *map[string]string) error {
	p, err := findInfoRegFilePath(dir, reg.Name)
	if err != nil {
		return err
	}
	// Round-trip: читаем существующий файл, чтобы сохранить поля, которые
	// форма не редактирует. Titles объекта перезаписываем только если форма
	// прислала блок переводов (objTitles != nil).
	if raw, rerr := os.ReadFile(p); rerr == nil {
		var existing saveInfoReg
		if yaml.Unmarshal(raw, &existing) == nil {
			reg.Title = existing.Title
			if objTitles == nil {
				reg.Titles = existing.Titles
			}
		}
	}
	if objTitles != nil {
		reg.Titles = *objTitles
	}
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	return os.WriteFile(p, out, 0o644)
}

func (h *handler) saveInfoRegToDB(ctx context.Context, b *Base, reg saveInfoReg, objTitles *map[string]string) error {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	rows, err := db.Query(ctx, `SELECT path, content FROM _onebase_config WHERE path LIKE 'inforegs/%.yaml'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var targetPath string
	for rows.Next() {
		var p string
		var content []byte
		if err := rows.Scan(&p, &content); err != nil {
			continue
		}
		var existing saveInfoReg
		if yaml.Unmarshal(content, &existing) == nil && existing.Name == reg.Name {
			targetPath = p
			// Round-trip: сохраняем поля, которые форма не редактирует.
			// Titles перезаписываем только если форма прислала блок переводов.
			reg.Title = existing.Title
			if objTitles == nil {
				reg.Titles = existing.Titles
			}
			break
		}
	}
	rows.Close()
	if targetPath == "" {
		return fmt.Errorf("inforeg %q not found in DB config", reg.Name)
	}
	if objTitles != nil {
		reg.Titles = *objTitles
	}
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	return cfgUpsert(ctx, db, targetPath, out)
}

func (h *handler) configuratorSaveInfoRegFields(w http.ResponseWriter, r *http.Request) {
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
	reg := saveInfoReg{
		Name:       r.FormValue("inforeg"),
		Periodic:   r.FormValue("periodic") == "true",
		Dimensions: parseRegSection(r, "dim"),
		Resources:  parseRegSection(r, "res"),
	}
	var objTitles *map[string]string
	if formHasMapField(r, "titles") {
		t := parseMapForm(r, "titles")
		objTitles = &t
	}
	if err := validateInfoRegFieldEdit(reg); err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка проверки") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.saveInfoRegToDB(r.Context(), b, reg, objTitles)
	} else {
		saveErr = saveInfoRegToFile(b.Path, reg, objTitles)
	}
	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = reg.Name
	}
	renderCfg(w, r, data)
}

// ── AccountRegister save ───────────────────────────────────────────────────────

func findAccountRegFilePath(dir, name string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "accountregs"))
	if err != nil {
		return "", fmt.Errorf("accountregs dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		p := filepath.Join(dir, "accountregs", e.Name())
		raw, _ := os.ReadFile(p)
		var tmp struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(raw, &tmp) == nil && tmp.Name == name {
			return p, nil
		}
	}
	return "", fmt.Errorf("accountreg %q not found", name)
}

func saveAccountRegToFile(dir string, reg saveAccountReg, setTitles bool) error {
	p, err := findAccountRegFilePath(dir, reg.Name)
	if err != nil {
		// новый файл — subconto/titles сохранять неоткуда, marshal свежего reg
		if merr := os.MkdirAll(filepath.Join(dir, "accountregs"), 0o755); merr != nil { //nolint:gosec // G301: права — соглашение пакета, разбор на этапе 109H
			return merr
		}
		p = filepath.Join(dir, "accountregs", nameToFilename(reg.Name)+".yaml")
		out, merr := yaml.Marshal(&reg)
		if merr != nil {
			return merr
		}
		return os.WriteFile(p, out, 0o644)
	}
	raw, rerr := os.ReadFile(p)
	if rerr != nil {
		return rerr
	}
	out, merr := applyAccountRegFields(raw, reg, setTitles)
	if merr != nil {
		return merr
	}
	return os.WriteFile(p, out, 0o644)
}

func (h *handler) saveAccountRegToDB(ctx context.Context, b *Base, reg saveAccountReg, setTitles bool) error {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	rows, err := db.Query(ctx, `SELECT path, content FROM _onebase_config WHERE path LIKE 'accountregs/%.yaml'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	targetPath := "accountregs/" + nameToFilename(reg.Name) + ".yaml"
	var existingContent []byte
	for rows.Next() {
		var p string
		var content []byte
		if err := rows.Scan(&p, &content); err != nil {
			continue
		}
		var tmp struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(content, &tmp) == nil && tmp.Name == reg.Name {
			targetPath = p
			existingContent = content
			break
		}
	}
	rows.Close()
	// Сохраняем subconto/titles из существующей записи через node-редактирование;
	// для новой записи marshal свежего reg.
	var out []byte
	if len(existingContent) > 0 {
		out, err = applyAccountRegFields(existingContent, reg, setTitles)
	} else {
		out, err = yaml.Marshal(&reg)
	}
	if err != nil {
		return err
	}
	return cfgUpsert(ctx, db, targetPath, out)
}

func (h *handler) configuratorSaveAccountRegister(w http.ResponseWriter, r *http.Request) {
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
	reg := saveAccountReg{
		Name:      r.FormValue("accountreg"),
		Title:     strings.TrimSpace(r.FormValue("title")),
		Accounts:  strings.TrimSpace(r.FormValue("accounts")),
		Resources: parseRegSection(r, "res"),
	}
	setTitles := formHasMapField(r, "titles")
	if setTitles {
		reg.Titles = parseMapForm(r, "titles")
	}
	if err := validateAccountRegFieldEdit(reg); err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка проверки") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.saveAccountRegToDB(r.Context(), b, reg, setTitles)
	} else {
		saveErr = saveAccountRegToFile(b.Path, reg, setTitles)
	}
	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = reg.Name
	}
	renderCfg(w, r, data)
}

// ── Predefined items save ─────────────────────────────────────────────────────
