package launcher

import (
	"fmt"
	"github.com/ivantit66/onebase/internal/fsmode"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"gopkg.in/yaml.v3"
)

func (h *handler) configuratorSaveEnum(w http.ResponseWriter, r *http.Request) {
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
	enumName := r.FormValue("enum_name")

	type enumValueOut struct {
		Name   string            `yaml:"name"`
		Titles map[string]string `yaml:"titles,omitempty"`
	}
	var values []any
	for i := 0; i < 500; i++ {
		name := strings.TrimSpace(r.FormValue(fmt.Sprintf("value.%d.name", i)))
		if name == "" {
			continue
		}
		titles := parseMapForm(r, fmt.Sprintf("value.%d.titles", i))
		if len(titles) == 0 {
			values = append(values, name)
		} else {
			values = append(values, enumValueOut{Name: name, Titles: titles})
		}
	}

	type saveEnumOut struct {
		Name   string `yaml:"name"`
		Values []any  `yaml:"values"`
	}
	out, _ := yaml.Marshal(saveEnumOut{Name: enumName, Values: values})

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			path := "enums/" + nameToFilename(enumName) + ".yaml"
			saveErr = cfgUpsert(r.Context(), db, path, out)
		}
	} else {
		dir := filepath.Join(b.Path, "enums")
		bestEffort("создать каталог перечислений", os.MkdirAll(dir, fsmode.Dir)) //nolint:gosec // G301: права — соглашение пакета, разбор на этапе 109H
		// find existing file by name field, fallback to name-based filename
		files, _ := os.ReadDir(dir)
		targetFile := filepath.Join(dir, nameToFilename(enumName)+".yaml")
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}
			p := filepath.Join(dir, f.Name())
			raw, _ := os.ReadFile(p)
			var hdr struct {
				Name string `yaml:"name"`
			}
			if yaml.Unmarshal(raw, &hdr) == nil && hdr.Name == enumName {
				targetFile = p
				break
			}
		}
		saveErr = os.WriteFile(targetFile, out, fsmode.File)
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = enumName
	}
	renderCfg(w, r, data)
}

// ── Constant save ─────────────────────────────────────────────────────────────

func (h *handler) configuratorSaveConstant(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if failForm(w, r) {
		return
	}
	constName := r.FormValue("const_name")
	label := strings.TrimSpace(r.FormValue("label"))
	typ := strings.TrimSpace(r.FormValue("type"))
	ref := strings.TrimSpace(r.FormValue("ref"))
	def := strings.TrimSpace(r.FormValue("default"))
	if typ == "reference" && ref != "" {
		typ = "reference:" + ref
	}
	// Разрядность числовой константы number(L,P) — как у реквизитов сущности.
	typ = numberTypeWithSpec(typ, r.FormValue("length"), r.FormValue("scale"))

	type rawConst struct {
		Name    string            `yaml:"name"`
		Type    string            `yaml:"type"`
		Default string            `yaml:"default,omitempty"`
		Label   string            `yaml:"label,omitempty"`
		Labels  map[string]string `yaml:"labels,omitempty"`
	}
	type rawConstsFile struct {
		Constants []rawConst `yaml:"constants"`
	}

	updateConstantsFile := func(raw []byte) ([]byte, error) {
		var cf rawConstsFile
		if err := yaml.Unmarshal(raw, &cf); err != nil {
			return nil, err
		}
		for i, c := range cf.Constants {
			if c.Name == constName {
				cf.Constants[i].Label = label
				cf.Constants[i].Type = typ
				cf.Constants[i].Default = def
				if formHasMapField(r, "labels") {
					cf.Constants[i].Labels = parseMapForm(r, "labels")
				}
				break
			}
		}
		return yaml.Marshal(&cf)
	}

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			// Поиск файла, в котором объявлена константа.
			//
			// Раньше здесь терялись три ошибки подряд, и каждая давала «успешно
			// сохранено» без сохранения: результат Query не проверялся (при
			// сбое rows == nil и следующий же Next паниковал), сбойный Scan
			// пропускал файл-кандидат, а если после этого ничего не нашлось —
			// saveErr оставался nil и пользователь получал отметку о записи.
			rows, qerr := db.Query(r.Context(),
				`SELECT path, content FROM _onebase_config WHERE path LIKE 'constants/%.yaml'`)
			if qerr != nil {
				saveErr = qerr
			} else {
				var targetPath string
				var targetContent []byte
				for rows.Next() {
					var p string
					var content []byte
					if serr := rows.Scan(&p, &content); serr != nil {
						saveErr = serr
						break
					}
					var cf rawConstsFile
					if yaml.Unmarshal(content, &cf) == nil {
						for _, c := range cf.Constants {
							if c.Name == constName {
								targetPath = p
								targetContent = content
								break
							}
						}
					}
					if targetPath != "" {
						break
					}
				}
				rows.Close()
				switch {
				case saveErr != nil:
				case rows.Err() != nil:
					saveErr = rows.Err()
				case targetPath == "":
					saveErr = i18nerr.Errorf("константа %s не найдена в конфигурации", constName)
				default:
					out, uerr := updateConstantsFile(targetContent)
					if uerr != nil {
						saveErr = uerr
					} else {
						saveErr = cfgUpsert(r.Context(), db, targetPath, out)
					}
				}
			}
		}
	} else {
		dir := filepath.Join(b.Path, "constants")
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}
			p := filepath.Join(dir, f.Name())
			raw, _ := os.ReadFile(p)
			var cf rawConstsFile
			if yaml.Unmarshal(raw, &cf) != nil {
				continue
			}
			found := false
			for _, c := range cf.Constants {
				if c.Name == constName {
					found = true
					break
				}
			}
			if !found {
				continue
			}
			out, err := updateConstantsFile(raw)
			if err == nil {
				saveErr = os.WriteFile(p, out, fsmode.File)
			} else {
				saveErr = err
			}
			break
		}
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = constName
	}
	renderCfg(w, r, data)
}

// ── Report save ───────────────────────────────────────────────────────────────
