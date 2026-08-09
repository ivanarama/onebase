package launcher

import (
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"gopkg.in/yaml.v3"
)

func (h *handler) configuratorSaveProcessor(w http.ResponseWriter, r *http.Request) {
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
	procName := r.FormValue("processor_name")
	title := strings.TrimSpace(r.FormValue("title"))
	source := r.FormValue("source")
	if !validObjectName(procName) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя обработки")
		renderCfg(w, r, data)
		return
	}

	type saveParam struct {
		Name   string            `yaml:"name"`
		Type   string            `yaml:"type"`
		Label  string            `yaml:"label,omitempty"`
		Labels map[string]string `yaml:"labels,omitempty"`
	}
	type saveProcessor struct {
		Name   string            `yaml:"name"`
		Title  string            `yaml:"title,omitempty"`
		Titles map[string]string `yaml:"titles,omitempty"`
		Params []saveParam       `yaml:"params,omitempty"`
	}

	// Индексы собираем скан-циклом formRowIndices, а не перебором подряд:
	// клиент считает номер новой строки по числу строк с текстовым полем, а
	// шаблон подкладывает под каждый параметр вторую строку с блоком переводов,
	// поэтому индексы разрежены (issue #677). Удаление параметра тоже оставляет
	// дыру. Плотный цикл обрывался на первой из них.
	var newParams []saveParam
	for _, i := range formRowIndices(r, "param") {
		pname := strings.TrimSpace(r.FormValue(fmt.Sprintf("param.%d.name", i)))
		if pname == "" {
			continue // строку очистили — считаем удалённой
		}
		ptype := r.FormValue(fmt.Sprintf("param.%d.type", i))
		plabel := strings.TrimSpace(r.FormValue(fmt.Sprintf("param.%d.label", i)))
		newParams = append(newParams, saveParam{
			Name: pname, Type: ptype, Label: plabel,
			Labels: parseMapForm(r, fmt.Sprintf("param.%d.labels", i)),
		})
	}

	// Переводы заголовка: гейт отличает «блок переводов не отрендерен» (не трогаем
	// существующее) от «пользователь очистил все переводы» (удаляем ключ titles).
	var newTitles map[string]string
	hasTitlesBlock := formHasMapField(r, "titles")
	if hasTitlesBlock {
		newTitles = parseMapForm(r, "titles")
	}

	// Round-trip node-edit (issue #3): правим только редактируемые в форме ключи
	// прямо в дереве YAML, чтобы не терять отсутствующие в форме блоки обработки —
	// table_parts и поля default/options параметров. Усечённый yaml.Marshal
	// типизированной saveProcessor молча их стирал (потеря данных). Параметры
	// сливаем по имени с существующими узлами, сохраняя их default/options.
	//
	// procMapField возвращает узел-значение ключа в mapping-узле (или nil).
	procMapField := func(m *yaml.Node, key string) *yaml.Node {
		if m == nil || m.Kind != yaml.MappingNode {
			return nil
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == key {
				return m.Content[i+1]
			}
		}
		return nil
	}
	// procSetSeq устанавливает (или удаляет при seq==nil) ключ key со значением-
	// последовательностью seq прямо в mapping-узле, сохраняя порядок прочих ключей.
	procSetSeq := func(m *yaml.Node, key string, seq *yaml.Node) {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == key {
				if seq == nil {
					m.Content = append(m.Content[:i], m.Content[i+2:]...)
					return
				}
				m.Content[i+1] = seq
				return
			}
		}
		if seq != nil {
			m.Content = append(m.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: key}, seq)
		}
	}
	updateProcessorFile := func(raw []byte) ([]byte, error) {
		var root yaml.Node
		if err := yaml.Unmarshal(raw, &root); err != nil {
			return nil, err
		}
		if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("updateProcessorFile: ожидалось YAML-отображение в корне обработки")
		}
		doc := root.Content[0]
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
		// Существующие узлы параметров по имени (lowercase) — для сохранения
		// неотображаемых в форме ключей (default/options).
		existing := map[string]*yaml.Node{}
		if old := procMapField(doc, "params"); old != nil && old.Kind == yaml.SequenceNode {
			for _, pn := range old.Content {
				if name := procMapField(pn, "name"); name != nil {
					existing[strings.ToLower(name.Value)] = pn
				}
			}
		}
		var paramSeq *yaml.Node
		if len(newParams) > 0 {
			paramSeq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, p := range newParams {
				node := existing[strings.ToLower(p.Name)]
				if node == nil {
					node = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
					if err := setYAMLMapField(node, "name", p.Name); err != nil {
						return nil, err
					}
				}
				if err := setYAMLMapField(node, "type", p.Type); err != nil {
					return nil, err
				}
				var label any
				if p.Label != "" {
					label = p.Label
				}
				if err := setYAMLMapField(node, "label", label); err != nil {
					return nil, err
				}
				if err := setYAMLMapField(node, "labels", anyOrNil(p.Labels)); err != nil {
					return nil, err
				}
				paramSeq.Content = append(paramSeq.Content, node)
			}
		}
		procSetSeq(doc, "params", paramSeq)
		return yaml.Marshal(&root)
	}

	yamlFilename := "processors/" + nameToFilename(procName) + ".yaml"
	srcFilename := "src/" + processorSrcFilename(procName)

	// Готовим YAML: round-trip из существующего файла (сохраняет table_parts и
	// прочее), либо полная сборка для новой обработки (терять нечего).
	buildYAML := func(raw []byte) []byte {
		if len(raw) > 0 {
			if out, err := updateProcessorFile(raw); err == nil {
				return out
			}
		}
		out, _ := yaml.Marshal(saveProcessor{
			Name: procName, Title: title, Params: newParams, Titles: newTitles,
		})
		return out
	}

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			existingYAML, _, _ := configdb.New(db).ReadFile(r.Context(), yamlFilename)
			yamlData := buildYAML(existingYAML)
			if err := saveConfigFiles(r, h, b, []configFileEntry{
				{relPath: yamlFilename, content: yamlData},
				{relPath: srcFilename, content: []byte(source)},
			}); err != nil {
				saveErr = err
			}
		}
	} else {
		existingYAML, _ := h.readConfigFileRaw(r.Context(), b, yamlFilename)
		yamlData := buildYAML(existingYAML)
		saveErr = saveConfigFiles(r, h, b, []configFileEntry{
			{relPath: yamlFilename, content: yamlData},
			{relPath: srcFilename, content: []byte(source)},
		})
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = procName
	}
	renderCfg(w, r, data)
}

func processorSrcFilename(name string) string {
	if name == "" {
		return ".proc.os"
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes) + ".proc.os"
}

// ── Print form save ───────────────────────────────────────────────────────────
