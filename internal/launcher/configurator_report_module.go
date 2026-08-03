package launcher

import (
	"fmt"
	"github.com/ivantit66/onebase/internal/fsmode"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/report"
	"gopkg.in/yaml.v3"
)

func (h *handler) configuratorSaveReport(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if failForm(w, r) {
		return
	}
	repName := r.FormValue("report_name")
	query := r.FormValue("query")
	title := strings.TrimSpace(r.FormValue("title"))
	chartProc := strings.TrimSpace(r.FormValue("chart_proc"))
	chartSource := r.FormValue("chart_source")

	type saveParam struct {
		Name   string            `yaml:"name"`
		Type   string            `yaml:"type"`
		Label  string            `yaml:"label,omitempty"`
		Labels map[string]string `yaml:"labels,omitempty"`
	}
	type saveReport struct {
		Name        string              `yaml:"name"`
		Title       string              `yaml:"title,omitempty"`
		Params      []saveParam         `yaml:"params,omitempty"`
		Query       string              `yaml:"query"`
		ChartProc   string              `yaml:"chart_proc,omitempty"`
		Composition *report.Composition `yaml:"composition,omitempty"`
	}

	// Parse params from form: param.0.name, param.0.type, param.0.label, ...
	var newParams []saveParam
	for i := 0; i < 50; i++ {
		pname := strings.TrimSpace(r.FormValue(fmt.Sprintf("param.%d.name", i)))
		if pname == "" {
			break
		}
		ptype := r.FormValue(fmt.Sprintf("param.%d.type", i))
		plabel := strings.TrimSpace(r.FormValue(fmt.Sprintf("param.%d.label", i)))
		newParams = append(newParams, saveParam{
			Name:   pname,
			Type:   ptype,
			Label:  plabel,
			Labels: parseMapForm(r, fmt.Sprintf("param.%d.labels", i)),
		})
	}

	// Переводы объекта: вычисляем до updateReportFile — нужен гейт, чтобы
	// отличить «форма не имела блока переводов» (AvailableLangs пуст) от
	// «пользователь очистил все переводы». Только во втором случае ключ titles: удаляется.
	var newTitles map[string]string
	hasTitlesBlock := formHasMapField(r, "titles")
	if hasTitlesBlock {
		newTitles = parseMapForm(r, "titles")
	}

	// Правим только редактируемые в форме ключи прямо в дереве YAML, чтобы не
	// терять прочие поля отчёта — многоязычные titles и любые будущие (раньше
	// round-trip через типизированную saveReport стирал titles, issue #86).
	updateReportFile := func(raw []byte) ([]byte, error) {
		var root yaml.Node
		if err := yaml.Unmarshal(raw, &root); err != nil {
			return nil, err
		}
		if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("updateReportFile: ожидалось YAML-отображение в корне отчёта")
		}
		doc := root.Content[0]
		if err := setYAMLMapField(doc, "query", query); err != nil {
			return nil, err
		}
		if title != "" { // пустой title не трогаем — сохраняем существующий
			if err := setYAMLMapField(doc, "title", title); err != nil {
				return nil, err
			}
		}
		if hasTitlesBlock {
			if err := setYAMLMapField(doc, "titles", anyOrNil(newTitles)); err != nil {
				return nil, err
			}
		}
		var cp any
		if chartProc != "" {
			cp = chartProc // пусто → ключ удаляется (как omitempty)
		}
		if err := setYAMLMapField(doc, "chart_proc", cp); err != nil {
			return nil, err
		}
		var pv any
		if len(newParams) > 0 {
			pv = newParams
		}
		if err := setYAMLMapField(doc, "params", pv); err != nil {
			return nil, err
		}
		if c, present := parseCompositionForm(r.Form); present {
			var cv any
			if c != nil {
				cv = c
			}
			if err := setYAMLMapField(doc, "composition", cv); err != nil {
				return nil, err
			}
		}
		return yaml.Marshal(&root)
	}

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			// Тот же разбор, что и при сохранении константы: непроверенный
			// Query (при сбое rows == nil и Next паникует), пропуск кандидата
			// на сбойном Scan и «ничего не нашли → saveErr остался nil» — три
			// пути к отметке «сохранено» без сохранения.
			rows, qerr := db.Query(r.Context(),
				`SELECT path, content FROM _onebase_config WHERE path LIKE 'reports/%.yaml'`)
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
					var rep saveReport
					if yaml.Unmarshal(content, &rep) == nil && rep.Name == repName {
						targetPath = p
						targetContent = content
						break
					}
				}
				rows.Close()
				switch {
				case saveErr != nil:
				case rows.Err() != nil:
					saveErr = rows.Err()
				case targetPath == "":
					saveErr = i18nerr.Errorf("отчёт %s не найден в конфигурации", repName)
				default:
					out, uerr := updateReportFile(targetContent)
					if uerr != nil {
						saveErr = uerr
					} else {
						saveErr = cfgUpsert(r.Context(), db, targetPath, out)
					}
				}
			}
		}
	} else {
		dir := filepath.Join(b.Path, "reports")
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}
			p := filepath.Join(dir, f.Name())
			raw, _ := os.ReadFile(p)
			var rep saveReport
			if yaml.Unmarshal(raw, &rep) != nil || rep.Name != repName {
				continue
			}
			out, err := updateReportFile(raw)
			if err == nil {
				saveErr = os.WriteFile(p, out, fsmode.File)
			} else {
				saveErr = err
			}
			break
		}
	}

	// Save chart .rep.os source if provided
	if chartSource != "" && b.ConfigSource == "file" {
		saveErr = h.writeConfigFileRaw(r.Context(), b, "src/"+repName+".rep.os", []byte(chartSource))
	}
	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = repName
	}
	renderCfg(w, r, data)
}

// ── Common module save ────────────────────────────────────────────────────────

func (h *handler) configuratorSaveCommonModule(w http.ResponseWriter, r *http.Request) {
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
	moduleName := r.FormValue("module_name")
	source := r.FormValue("source")
	if !validObjectName(moduleName) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя модуля")
		renderCfg(w, r, data)
		return
	}

	filename := moduleNameToFilename(moduleName)

	var saveErr error
	if b.ConfigSource == "database" {
		db, err := OpenDB(r.Context(), b)
		if err != nil {
			saveErr = err
		} else {
			defer db.Close()
			saveErr = cfgUpsert(r.Context(), db, "src/"+filename, []byte(source))
		}
	} else {
		saveErr = h.writeConfigFileRaw(r.Context(), b, "src/"+filename, []byte(source))
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.ModuleSaved = true
		data.ModuleSavedEntity = moduleName
	}
	renderCfg(w, r, data)
}

func moduleNameToFilename(name string) string {
	if name == "" {
		return ".module.os"
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes) + ".module.os"
}

// ── Processor save ────────────────────────────────────────────────────────────
