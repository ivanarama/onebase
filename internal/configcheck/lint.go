package configcheck

import (
	"fmt"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/token"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"gopkg.in/yaml.v3"
)

// CheckLintYAML reports YAML keys that are accepted by yaml.v3 but ignored by
// the metadata loaders. These are warnings: they do not make a configuration
// invalid, but they almost always mean a typo or an expectation the platform
// currently does not implement.
func CheckLintYAML(dir string) []Issue {
	var issues []Issue
	for _, spec := range yamlLintSpecs() {
		root := filepath.Join(dir, spec.dir)
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
				continue
			}
			label := filepath.ToSlash(filepath.Join(spec.dir, e.Name()))
			issues = append(issues, lintYAMLFile(filepath.Join(root, e.Name()), label, spec.kind, spec.schema)...)
		}
	}

	issues = append(issues,
		lintYAMLFile(filepath.Join(dir, "config", "home_page.yaml"), "config/home_page.yaml", "Главная страница", homePageYAMLSchema())...)

	formsRoot := filepath.Join(dir, "forms")
	walkErr := filepath.WalkDir(formsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".form.yaml") {
			return nil
		}
		label := relLabel(dir, path)
		issues = append(issues, lintYAMLFile(path, label, "Управляемая форма", formModuleYAMLSchema())...)
		issues = append(issues, lintFormHotkeys(path, label)...)
		issues = append(issues, lintFormDeleteEvents(path, label)...)
		return nil
	})
	if walkErr != nil {
		oblog.Component("configcheck").Debug("обход каталога прерван", "err", walkErr)
	}

	return issues
}

// CheckLintProject reports advisory checks that require a successfully loaded
// project: DSL usage/reachability and role coverage.
func CheckLintProject(dir string, proj *project.Project, roles []*auth.Role) []Issue {
	var issues []Issue
	issues = append(issues, CheckLintDSL(dir, proj)...)
	issues = append(issues, CheckLintRoles(dir, proj, roles)...)
	issues = append(issues, CheckLintIndexes(proj)...)
	return issues
}

type yamlLintSpec struct {
	dir    string
	kind   string
	schema *yamlLintSchema
}

type yamlLintSchema struct {
	keys map[string]*yamlLintSchema
	elem *yamlLintSchema
	free bool
}

func yamlLintSpecs() []yamlLintSpec {
	return []yamlLintSpec{
		{"catalogs", "Справочник", entityYAMLSchema()},
		{"documents", "Документ", entityYAMLSchema()},
		{"registers", "Регистр", registerYAMLSchema()},
		{"inforegs", "Регистр сведений", infoRegisterYAMLSchema()},
		{"enums", "Перечисление", enumYAMLSchema()},
		{"constants", "Константы", constantsYAMLSchema()},
		{"widgets", "Виджет", widgetYAMLSchema()},
		{"reports", "Отчёт", reportYAMLSchema()},
		{"roles", "Роль", roleYAMLSchema()},
		{"processors", "Обработка", processorYAMLSchema()},
		{"services", "HTTP-сервис", serviceYAMLSchema()},
		{"pages", "Страница", pageYAMLSchema()},
		{"journals", "Журнал", journalYAMLSchema()},
		{"subsystems", "Подсистема", subsystemYAMLSchema()},
		{"scheduled", "Регламентное задание", scheduledYAMLSchema()},
		{"accounts", "План счетов", accountsYAMLSchema()},
		{"accountregs", "Регистр бухгалтерии", accountRegisterYAMLSchema()},
	}
}

func lintYAMLFile(path, label, kind string, schema *yamlLintSchema) []Issue {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil // YAML syntax errors are already reported by normal check.
	}
	if len(doc.Content) == 0 {
		return nil
	}
	var issues []Issue
	lintYAMLNode(label, kind, "", doc.Content[0], schema, &issues)
	return issues
}

func lintYAMLNode(label, kind, path string, node *yaml.Node, schema *yamlLintSchema, issues *[]Issue) {
	if node == nil || schema == nil || schema.free {
		return
	}
	if schema.elem != nil {
		if node.Kind != yaml.SequenceNode {
			return
		}
		nextPath := path + "[]"
		for _, item := range node.Content {
			lintYAMLNode(label, kind, nextPath, item, schema.elem, issues)
		}
		return
	}
	if node.Kind != yaml.MappingNode || len(schema.keys) == 0 {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]
		key := keyNode.Value
		child, ok := schema.keys[key]
		nextPath := key
		if path != "" {
			nextPath = path + "." + key
		}
		if !ok {
			*issues = append(*issues, Issue{
				File:         label,
				Kind:         kind,
				Code:         "metadata.unvalidated-key",
				Line:         keyNode.Line,
				Column:       keyNode.Column,
				Message:      fmt.Sprintf("неизвестный YAML-ключ %q: загрузчик его игнорирует", nextPath),
				SuggestedFix: "Удалите ключ, исправьте опечатку или добавьте поддержку этого поля в загрузчик метаданных.",
			})
			continue
		}
		lintYAMLNode(label, kind, nextPath, valueNode, child, issues)
	}
}

type formHotkeyRef struct {
	name string
	line int
}

// formDeleteEvents — события удаления, объявляемые в форме. Обработчик формы
// срабатывает только при удалении из открытой формы, а удаляют ещё из списка,
// пачкой, из DSL и по REST. Запрет, написанный только в форме, обходится
// сменой способа удаления — то есть не защищает. Настоящий гейт живёт в модуле
// объекта (Процедура ПередУдалением), и предупреждение об этом говорит.
var formDeleteEvents = map[string]string{
	"ПередУдалением": "ПередУдалением",
	"ПриУдалении":    "ПередУдалением",
	"ПослеУдаления":  "ПослеУдаления",
}

// lintFormDeleteEvents предупреждает, что форменный обработчик удаления
// закрывает один путь из пяти.
func lintFormDeleteEvents(path, label string) []Issue {
	data, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	events := yamlMapValue(doc.Content[0], "events")
	if events == nil || events.Kind != yaml.MappingNode {
		return nil
	}
	var issues []Issue
	for i := 0; i+1 < len(events.Content); i += 2 {
		name := strings.TrimSpace(events.Content[i].Value)
		moduleEvent, isDelete := formDeleteEvents[name]
		if !isDelete {
			continue
		}
		issues = append(issues, Issue{
			File:         path,
			Object:       label,
			Kind:         "Управляемая форма",
			Code:         "forms.delete-event-in-form",
			Message:      fmt.Sprintf("событие %q объявлено в форме: оно сработает только при удалении из открытой формы, а из списка, пачкой, из DSL и по REST объект удалится без него", name),
			SuggestedFix: fmt.Sprintf("Перенесите проверку в модуль объекта: Процедура %s() в src/<объект>.os — она выполняется на всех путях удаления.", moduleEvent),
		})
	}
	return issues
}

func lintFormHotkeys(path, label string) []Issue {
	data, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	elements := yamlMapValue(doc.Content[0], "elements")
	if elements == nil {
		return nil
	}
	seen := map[string]formHotkeyRef{}
	var issues []Issue
	lintFormHotkeyElements(label, elements, seen, &issues)
	return issues
}

func lintFormHotkeyElements(label string, node *yaml.Node, seen map[string]formHotkeyRef, issues *[]Issue) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return
	}
	for _, el := range node.Content {
		if el == nil || el.Kind != yaml.MappingNode {
			continue
		}
		kind := yamlMapScalar(el, "kind")
		name := yamlMapScalar(el, "name")
		if name == "" {
			name = kind
		}
		if hotkeyNode := yamlMapValue(el, "hotkey"); hotkeyNode != nil && strings.TrimSpace(hotkeyNode.Value) != "" {
			hotkey := strings.TrimSpace(hotkeyNode.Value)
			normalized := normalizeFormHotkey(hotkey)
			if kind != string(metadata.FormElementButton) {
				*issues = append(*issues, Issue{
					File:         label,
					Kind:         "Управляемая форма",
					Code:         "form.ignored-hotkey",
					Line:         hotkeyNode.Line,
					Column:       hotkeyNode.Column,
					Message:      fmt.Sprintf("hotkey %q у элемента %q игнорируется: сейчас hotkey поддержан только для kind: Кнопка", hotkey, name),
					SuggestedFix: "Используйте `accesskey` для полей ввода или перенесите `hotkey` на кнопку формы.",
				})
			} else if normalized == "" {
				*issues = append(*issues, Issue{
					File:         label,
					Kind:         "Управляемая форма",
					Code:         "form.unsupported-hotkey",
					Line:         hotkeyNode.Line,
					Column:       hotkeyNode.Column,
					Message:      fmt.Sprintf("hotkey %q у кнопки %q не поддержан runtime: доступны F2, F4, F7, F8, F9, F10", hotkey, name),
					SuggestedFix: "Выберите одну из поддержанных F-клавиш: F2, F4, F7, F8, F9, F10.",
				})
			} else if prev, ok := seen[normalized]; ok {
				*issues = append(*issues, Issue{
					File:         label,
					Kind:         "Управляемая форма",
					Code:         "form.duplicate-hotkey",
					Line:         hotkeyNode.Line,
					Column:       hotkeyNode.Column,
					Message:      fmt.Sprintf("hotkey %s у кнопки %q уже используется кнопкой %q на строке %d", normalized, name, prev.name, prev.line),
					SuggestedFix: "Оставьте одну кнопку на эту клавишу или назначьте другой hotkey.",
				})
			} else {
				seen[normalized] = formHotkeyRef{name: name, line: hotkeyNode.Line}
			}
		}
		lintFormHotkeyElements(label, yamlMapValue(el, "children"), seen, issues)
	}
}

func normalizeFormHotkey(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "F2", "F4", "F7", "F8", "F9", "F10":
		return strings.ToUpper(strings.TrimSpace(value))
	default:
		return ""
	}
}

func yamlMapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i] != nil && node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func yamlMapScalar(node *yaml.Node, key string) string {
	value := yamlMapValue(node, key)
	if value == nil {
		return ""
	}
	return strings.TrimSpace(value.Value)
}

func obj(keys ...string) *yamlLintSchema {
	m := make(map[string]*yamlLintSchema, len(keys))
	for _, k := range keys {
		m[k] = nil
	}
	return &yamlLintSchema{keys: m}
}

func seq(elem *yamlLintSchema) *yamlLintSchema {
	return &yamlLintSchema{elem: elem}
}

func freeMap() *yamlLintSchema {
	return &yamlLintSchema{free: true}
}

func with(base *yamlLintSchema, nested map[string]*yamlLintSchema) *yamlLintSchema {
	for k, v := range nested {
		base.keys[k] = v
	}
	return base
}

func fieldYAMLSchema(allowRequired bool) *yamlLintSchema {
	// `id` — устойчивый идентификатор реквизита (план 81). Он НЕ декоративный:
	// именно по нему миграция отличает переименование от «удалили одно поле,
	// добавили другое», а PlanTableChanges строит по нему сторож от тихой
	// потери колонки. Линт объявлял его неизвестным ключом («загрузчик его
	// игнорирует») и советовал удалить — совет прямо противоречил и загрузчику,
	// который его честно читает, и DEVELOPER.md, где id описан как
	// рекомендуемая практика. Пользователь, послушавшийся линта, снимал
	// страховку от потери данных (#873, дефект Д11 из #668).
	keys := []string{"id", "name", "title", "label", "type", "allow_inline_create"}
	if allowRequired {
		// Required is currently a write invariant for entity headers and table
		// parts. Register recorders have a different persistence path and must
		// not silently accept a declaration they do not enforce.
		keys = append(keys, "required")
		// default (план 153) гейтится тем же признаком и по той же причине:
		// объявление имеет силу только там, где его кто-то исполняет. У шапки
		// объекта дефолт применяется при создании; у реквизита ТЧ ключ читается
		// тем же parseField, но ValidateDefaults отвергает его отдельным
		// сообщением — линту здесь придираться не к чему, ошибку выдаст check.
		// У измерений и ресурсов регистров дефолт не применяется вовсе, и там
		// «неизвестный ключ» — правда: значение действительно никуда не идёт.
		keys = append(keys, "default")
	}
	return with(obj(keys...), map[string]*yamlLintSchema{
		"titles": freeMap(),
	})
}

func tablePartYAMLSchema(allowRequired bool) *yamlLintSchema {
	return with(obj("name", "title"), map[string]*yamlLintSchema{
		"titles": freeMap(),
		"fields": seq(fieldYAMLSchema(allowRequired)),
	})
}

func indexYAMLSchema() *yamlLintSchema {
	return obj("fields", "unique")
}

func entityYAMLSchema() *yamlLintSchema {
	detailTab := with(obj("name", "fields", "tableparts", "attachments"), map[string]*yamlLintSchema{
		"titles": freeMap(),
	})
	detailPanel := with(obj("title", "width", "fields"), map[string]*yamlLintSchema{
		"tabs": seq(detailTab),
	})
	// Этапы (план 121): deadline_days — свободная карта «этап → дни», её ключи
	// проверяет metadata.Validate по значениям перечисления.
	stages := with(obj("field", "order", "enforce"), map[string]*yamlLintSchema{
		"transitions":   seq(obj("from", "to")),
		"deadline_days": freeMap(),
	})
	return with(obj(
		"name", "title", "description", "posting", "hierarchical", "hierarchy_kind",
		"presentation",
		"list_form", "item_form", "based_on", "list_mode", "notify_changes", "list_refresh_on",
		"fulltext", "search_fields", "detail_panel",
	), map[string]*yamlLintSchema{
		"titles": freeMap(),
		// item_form принимает и строку (имя реквизита), и запись
		// {name, readonly} — «показывать, но не давать править» (#1011).
		// Скалярные элементы схема пропускает, а опечатку в ключе записи
		// (read_only) ловит.
		"item_form":    seq(obj("name", "readonly")),
		"fields":       seq(fieldYAMLSchema(true)),
		"tableparts":   seq(tablePartYAMLSchema(true)),
		"indexes":      seq(indexYAMLSchema()),
		"numerator":    obj("prefix", "length", "period", "scope", "base_prefix", "unique"),
		"predefined":   seq(with(obj("name"), map[string]*yamlLintSchema{"fields": freeMap()})),
		"tile_view":    obj("image", "title", "subtitle", "fields"),
		"activity":     obj("field", "default_scope", "hide_from_choice"),
		"detail_panel": detailPanel,
		"stages":       stages,
	})
}

func registerYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title", "kind"), map[string]*yamlLintSchema{
		"titles":     freeMap(),
		"dimensions": seq(fieldYAMLSchema(false)),
		"resources":  seq(fieldYAMLSchema(false)),
		"attributes": seq(fieldYAMLSchema(false)),
	})
}

func infoRegisterYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title", "periodic", "recorder"), map[string]*yamlLintSchema{
		"titles":     freeMap(),
		"dimensions": seq(fieldYAMLSchema(false)),
		"resources":  seq(fieldYAMLSchema(false)),
	})
}

func enumYAMLSchema() *yamlLintSchema {
	return with(obj("name"), map[string]*yamlLintSchema{
		"values": seq(with(obj("name"), map[string]*yamlLintSchema{"titles": freeMap()})),
	})
}

func constantsYAMLSchema() *yamlLintSchema {
	return with(obj(), map[string]*yamlLintSchema{
		"constants": seq(with(obj("name", "type", "default", "label"), map[string]*yamlLintSchema{"labels": freeMap()})),
	})
}

func widgetYAMLSchema() *yamlLintSchema {
	return with(obj("name", "type", "title", "query", "format", "compare_to", "limit", "chart_kind", "chart_type", "x_field", "y_fields", "entities", "scope", "link"), map[string]*yamlLintSchema{
		"titles": freeMap(),
		"params": freeMap(),
		"columns": seq(with(obj("field", "label", "format", "align"), map[string]*yamlLintSchema{
			"labels": freeMap(),
		})),
		"items": seq(with(obj("label", "entity", "url", "new_tab"), map[string]*yamlLintSchema{
			"labels": freeMap(),
		})),
	})
}

func reportYAMLSchema() *yamlLintSchema {
	measure := obj("field", "agg", "title", "align", "format", "expr")
	sortKey := obj("field", "dir")
	style := obj("color", "background", "bold", "italic")
	conditional := with(obj("when", "field"), map[string]*yamlLintSchema{"style": style})
	composition := with(obj("groupings", "columns", "detail", "detail_link", "detail_entity"), map[string]*yamlLintSchema{
		"measures":    seq(measure),
		"totals":      obj("grand", "subtotals"),
		"sort":        seq(sortKey),
		"conditional": seq(conditional),
		"appearance":  obj("lines", "zebra", "collapse_to"),
		"chart":       obj("type", "category", "series"),
	})
	return with(obj("name", "title", "query", "chart_proc", "output_format"), map[string]*yamlLintSchema{
		"titles": freeMap(),
		// У отчёта «required» НЕТ намеренно: report.Param такого поля не знает,
		// и разрешённый линтом ключ молча ничего бы не делал. Обязательность
		// параметра пока только у обработок (processor.Param).
		"params":      seq(with(obj("name", "type", "label", "options"), map[string]*yamlLintSchema{"labels": freeMap()})),
		"composition": composition,
		"variants":    seq(with(obj("name"), map[string]*yamlLintSchema{"composition": composition})),
	})
}

func roleYAMLSchema() *yamlLintSchema {
	perm := with(obj(), map[string]*yamlLintSchema{
		"ai_data_access": freeMap(),
		"catalogs":       freeMap(),
		"documents":      freeMap(),
		"registers":      freeMap(),
		"inforegs":       freeMap(),
		"reports":        freeMap(),
		"processors":     freeMap(),
		"row_access":     freeMap(),
		"field_access":   freeMap(),
	})
	return with(obj("name", "description"), map[string]*yamlLintSchema{"permissions": perm})
}

func processorYAMLSchema() *yamlLintSchema {
	param := with(obj("name", "type", "label", "default", "options", "required"), map[string]*yamlLintSchema{"labels": freeMap()})
	return with(obj("name", "title", "kind"), map[string]*yamlLintSchema{
		"titles": freeMap(),
		"params": seq(param),
		// Processor table parts are transient runtime values. Unlike entity table
		// parts they have no storage writer enforcing required, so accepting the
		// key here would advertise an invariant the platform does not provide.
		"table_parts": seq(tablePartYAMLSchema(false)),
	})
}

func serviceYAMLSchema() *yamlLintSchema {
	cors := obj("origins", "headers", "credentials", "max_age")
	template := with(obj("template"), map[string]*yamlLintSchema{"methods": freeMap()})
	// План 126: кэш ответов. План 128: сжатие и заголовки безопасности.
	cache := obj("ttl", "vary", "public", "max_body")
	securityHeaders := with(obj("csp", "frame_options", "referrer_policy", "hsts"),
		map[string]*yamlLintSchema{"extra": freeMap()})
	return with(obj("name", "title", "root_url", "auth", "secret", "rate_limit", "roles", "compress"),
		map[string]*yamlLintSchema{
			"titles":           freeMap(),
			"cors":             cors,
			"cache":            cache,
			"security_headers": securityHeaders,
			"templates":        seq(template),
		})
}

func pageYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title", "icon", "roles", "params"), map[string]*yamlLintSchema{"titles": freeMap()})
}

func journalYAMLSchema() *yamlLintSchema {
	column := with(obj("field", "label", "fallback", "format"), map[string]*yamlLintSchema{
		"labels": freeMap(),
		"map":    freeMap(),
	})
	style := obj("color", "background", "bold", "italic")
	conditional := with(obj("when", "field"), map[string]*yamlLintSchema{
		"style": style,
		"then":  style,
	})
	return with(obj("name", "title", "documents"), map[string]*yamlLintSchema{
		"titles":                 freeMap(),
		"columns":                seq(column),
		"filters":                seq(with(obj("field", "label", "type"), map[string]*yamlLintSchema{"labels": freeMap()})),
		"conditional":            seq(conditional),
		"conditional_formatting": seq(conditional),
	})
}

func subsystemYAMLSchema() *yamlLintSchema {
	contents := obj("documents", "catalogs", "reports", "inforegs", "registers", "processors", "journals", "pages")
	return with(obj("name", "title", "icon", "order", "roles"), map[string]*yamlLintSchema{
		"titles":    freeMap(),
		"contents":  contents,
		"home_page": homePageYAMLSchema(),
	})
}

func scheduledYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title", "schedule", "processor", "enabled", "on_error", "timeout"), map[string]*yamlLintSchema{
		"titles": freeMap(),
		"params": freeMap(),
	})
}

func accountsYAMLSchema() *yamlLintSchema {
	account := with(obj("code", "name", "kind", "parent"), map[string]*yamlLintSchema{"names": freeMap()})
	return with(obj("name", "title"), map[string]*yamlLintSchema{
		"titles":   freeMap(),
		"accounts": seq(account),
	})
}

func accountRegisterYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title", "accounts"), map[string]*yamlLintSchema{
		"titles":    freeMap(),
		"resources": seq(fieldYAMLSchema(false)),
		"subconto":  seq(fieldYAMLSchema(false)),
	})
}

func homePageYAMLSchema() *yamlLintSchema {
	nav := obj("documents", "catalogs", "reports", "inforegs", "registers", "processors", "journals", "pages")
	return with(obj("title", "layout", "hidden"), map[string]*yamlLintSchema{
		"titles":  freeMap(),
		"rows":    seq(obj("widgets")),
		"widgets": seq(obj("name", "span")),
		"nav":     nav,
	})
}

func formModuleYAMLSchema() *yamlLintSchema {
	element := &yamlLintSchema{}
	element.keys = map[string]*yamlLintSchema{}
	for _, k := range []string{
		"id", "name", "kind", "field", "table_part", "visible", "enabled", "required",
		"original_id", "data_path", "picture", "values_picture", "width", "height",
		"halign", "valign", "readonly", "readonly_when", "hidden_when", "use_grid", "no_grid", "auto_sum", "hint", "mask",
		"accesskey", "hotkey", "multiline", "format", "display_format", "type", "choice", "unknown_xml", "view",
		// Ключи, поддержанные загрузчиком, но забытые здесь: линт объявлял их
		// неизвестными, а гейт CI считает предупреждение ошибкой — то есть
		// документированный «language» у kind: ПолеКода не давал примеру
		// пройти собственную проверку (#1014).
		"orientation", "input_mask", "language", "virtual_columns",
	} {
		element.keys[k] = nil
	}
	element.keys["title"] = freeMap()
	element.keys["events"] = freeMap()
	element.keys["props"] = freeMap()
	element.keys["children"] = seq(element)
	element.keys["choices"] = seq(with(obj("value"), map[string]*yamlLintSchema{"title": freeMap()}))
	element.keys["options"] = seq(with(obj("value"), map[string]*yamlLintSchema{"label": freeMap()}))

	attrColumn := with(obj("id", "original_id", "name", "type", "length", "precision"), map[string]*yamlLintSchema{
		"title": freeMap(),
		"props": freeMap(),
	})
	attr := with(obj("id", "original_id", "name", "type", "length", "precision", "allowed_length", "save", "filling_value", "main"), map[string]*yamlLintSchema{
		"title":   freeMap(),
		"columns": seq(attrColumn),
		"props":   freeMap(),
	})
	command := with(obj("id", "original_id", "name", "group", "picture", "action"), map[string]*yamlLintSchema{
		"title": freeMap(),
		"props": freeMap(),
	})
	button := with(obj("id", "original_id", "name", "command", "representation", "picture"), map[string]*yamlLintSchema{
		"title": freeMap(),
	})
	commandBar := obj("id", "original_id", "name", "visible")
	commandBar.keys["buttons"] = seq(button)

	formHeader := with(obj("entity", "name", "kind", "original_id", "auto_save_settings", "auto_save_data_in_settings", "vertical_scroll"), map[string]*yamlLintSchema{
		"title": freeMap(),
	})
	style := obj("color", "background", "bold", "italic")
	conditional := with(obj("when", "target", "element", "table_part", "field"), map[string]*yamlLintSchema{
		"style": style,
		"then":  style,
	})

	return with(obj("schema", "entity", "name", "kind", "layout_kind", "original_id", "auto_save_settings", "auto_save_data_in_settings", "vertical_scroll"), map[string]*yamlLintSchema{
		"form":                   formHeader,
		"title":                  freeMap(),
		"events":                 freeMap(),
		"elements":               seq(element),
		"actions":                freeMap(),
		"attributes":             seq(attr),
		"commands":               seq(command),
		"command_bar":            commandBar,
		"conditional":            seq(conditional),
		"conditional_formatting": seq(conditional),
		"oneC_meta":              freeMap(),
	})
}

type lintProgram struct {
	label       string
	object      string
	kind        string
	prog        *ast.Program
	roots       map[string]bool
	rootAll     bool
	testContext bool
	// tpContextProcs — процедуры, привязанные к событию табличной части или её
	// колонки. Только им платформа инжектирует ТекущаяСтрока и соседей, поэтому
	// держать эти имена в commonDSLGlobals нельзя: опечатка в обычном модуле
	// перестала бы ловиться (та же причина, что у testDSLGlobals).
	tpContextProcs map[string]bool
}

// CheckLintDSL reports declared but unread DSL variables and procedures that
// are unreachable from known runtime entry points.
func CheckLintDSL(dir string, proj *project.Project) []Issue {
	programs := collectLintPrograms(dir, proj)
	globals := knownGlobalNames(proj)
	var issues []Issue
	for _, lp := range programs {
		issues = append(issues, lintUnusedVars(lp)...)
		issues = append(issues, lintCrossScopeReads(lp)...)
		issues = append(issues, lintUnknownGlobalMembers(lp, globals)...)
	}
	issues = append(issues, lintDeadProcedures(programs)...)
	return issues
}

// knownGlobalNames — имена, у которых обращение к полю заведомо осмысленно:
// общие модули (Модуль.Процедура) и builtin-имена платформы. Дополняет
// commonDSLGlobals, который перечисляет
// инжектируемые объекты-значения.
func knownGlobalNames(proj *project.Project) map[string]bool {
	out := map[string]bool{}
	if proj != nil {
		// Только общие модули доступны через обычный синтаксис
		// Модуль.Процедура(). Объектные, manager-, service- и page-программы
		// живут в отдельных runtime namespace и не являются глобальными
		// объектами DSL.
		for name := range proj.Modules {
			out[strings.ToLower(name)] = true
		}
	}
	for name := range interpreter.KnownBuiltinNames() {
		out[name] = true
	}
	return out
}

// CheckStrictLexicalScope reports DSL dependencies that are incompatible with
// dsl.strict_lexical_scope. Unlike CheckLintDSL, these issues are blocking:
// strict runtime will not expose caller-local variables to helper procedures.
func CheckStrictLexicalScope(dir string, proj *project.Project) []Issue {
	programs := collectLintPrograms(dir, proj)
	var issues []Issue
	for _, lp := range programs {
		for _, is := range lintCrossScopeReads(lp) {
			is.Message += "; при dsl.strict_lexical_scope: true это блокирующая ошибка"
			is.SuggestedFix = "Передайте значение параметром/результатом функции или объявите переменную локально: строгий режим не даёт процедуре читать локальные переменные вызывающей."
			issues = append(issues, is)
		}
	}
	return issues
}

// commonDSLGlobals — инжектируемые объекты-значения, доступные в любом модуле без
// объявления (dslvars.Common.Build + контекстные переменные форм/заданий). Они
// читаются как значения, поэтому попадают в reads; исключаем, чтобы не спутать с
// переменными. Builtins-функции в список не нужны: вызов callee не считается
// чтением (см. collectReadIdentTokensExpr).
var commonDSLGlobals = map[string]bool{
	"документы": true, "documents": true,
	"справочники": true, "catalogs": true,
	"перечисления": true, "enums": true,
	"константы": true, "constants": true,
	"движения": true, "movements": true,
	"символы": true, "chars": true,
	"запрос": true, "query": true,
	"предопределённыезначения": true, "предопределенныезначения": true, "predefinedvalues": true,
	"регистрынакопления": true, "регистрысведений": true, "регистрыбухгалтерии": true,
	"планысчетов": true, "планывидовхарактеристик": true,
	"ссылканаобъект": true, "objectref": true,
	"этотобъект": true, "this": true,
	// Контекст форм/страниц/заданий/сервисов.
	"объект": true, "форма": true, "элементы": true, "элементыформы": true,
	"отказ": true, "параметры": true, "параметрысеанса": true, "запрос_": true,
}

// formTPContextGlobals — контекст события табличной части, доступный только
// внутри её обработчиков. Словарь имён живёт в metadata рядом с событиями форм,
// значения инжектирует рантайм; расхождение сторожит тест в internal/ui.
var formTPContextGlobals = func() map[string]bool {
	set := map[string]bool{}
	for _, name := range metadata.FormTablePartContextVars() {
		set[strings.ToLower(name)] = true
	}
	return set
}()

// testDSLGlobals инжектируются только в обработки с kind: test. Держать их в
// commonDSLGlobals нельзя: иначе опечатка/неподдерживаемый Мок.Email в обычном
// прикладном модуле будет ошибочно считаться допустимым глобалом.
var testDSLGlobals = map[string]bool{
	"утверждать": true, "assert": true,
	"мок": true, "mock": true,
	"часы": true, "clock": true,
}

// lintCrossScopeReads помечает чтение идентификатора, который процедура не
// объявляла локально, но который является локальной переменной (параметром,
// Перем, переменной цикла или целью присваивания) ДРУГОЙ процедуры того же
// модуля. Такое чтение сегодня резолвится только потому, что окружение вызванной
// процедуры сцеплено с окружением вызывающей (динамическая видимость чтения):
// код хрупкий и сломается при корректной лексической изоляции (см. план изоляции
// scope). Это предупреждение — рантайм не меняется.
func lintCrossScopeReads(lp lintProgram) []Issue {
	if lp.prog == nil || len(lp.prog.Procedures) < 2 {
		return nil
	}
	moduleVars := map[string]bool{}
	for _, decl := range lp.prog.ModuleVars {
		for _, tok := range decl.Names {
			moduleVars[strings.ToLower(tok.Literal)] = true
		}
	}
	procNames := map[string]bool{}
	for _, pr := range lp.prog.Procedures {
		procNames[strings.ToLower(pr.Name.Literal)] = true
	}
	// procLocals[i] — имена, «принадлежащие» i-й процедуре; ownerCount — в
	// скольких процедурах имя объявлено/присвоено.
	procLocals := make([]map[string]bool, len(lp.prog.Procedures))
	ownerCount := map[string]int{}
	for i, pr := range lp.prog.Procedures {
		ls := map[string]bool{}
		for _, p := range pr.Params {
			ls[strings.ToLower(p.Literal)] = true
		}
		collectDeclaredAndAssigned(pr.Body, ls)
		procLocals[i] = ls
		for name := range ls {
			ownerCount[name]++
		}
	}
	var issues []Issue
	for i, pr := range lp.prog.Procedures {
		reads := map[string]token.Token{}
		for _, def := range pr.Defaults {
			collectReadIdentTokensExpr(def, reads)
		}
		collectReadIdentTokensStmts(pr.Body, reads)
		names := make([]string, 0, len(reads))
		for name := range reads {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if procLocals[i][name] || moduleVars[name] || procNames[name] || commonDSLGlobals[name] {
				continue
			}
			// Имя — локальная другой процедуры (текущая исключена проверкой выше).
			if ownerCount[name] > 0 {
				issues = append(issues, crossScopeReadIssue(lp, reads[name]))
			}
		}
	}
	return issues
}

// lintUnknownGlobalMembers помечает обращение к полю идентификатора, которого
// нет нигде: ни локальная переменная, ни переменная модуля, ни процедура, ни
// инжектируемый глобал, ни модуль, ни builtin.
//
// Это отдельный класс молчаливой порчи данных, который не ловит ни одна из
// имеющихся проверок. `dsl.unknown-function` смотрит только на callee вызова, а
// «Символы.ПС» — это чтение поля неизвестного идентификатора: интерпретатор
// отдаёт nil, конкатенация превращает его в строку «<nil>», и она уезжает в
// данные. Ни ошибки, ни предупреждения — узнаёшь по испорченному результату.
// Ровно так в переносимой конфигурации оказалось 56 мест с «<nil>» вместо
// перевода строки.
//
// Голое чтение неизвестного имени не помечается: набор контекстных переменных
// зависит от вида исполнения (формы, задания, сервисы), и ложных срабатываний
// было бы больше пользы. Обращение к ПОЛЮ — сигнал куда более определённый:
// автор рассчитывал на объект, а объекта нет.
func lintUnknownGlobalMembers(lp lintProgram, globals map[string]bool) []Issue {
	if lp.prog == nil {
		return nil
	}
	moduleVars := map[string]bool{}
	for _, decl := range lp.prog.ModuleVars {
		for _, tok := range decl.Names {
			moduleVars[strings.ToLower(tok.Literal)] = true
		}
	}
	procNames := map[string]bool{}
	for _, pr := range lp.prog.Procedures {
		procNames[strings.ToLower(pr.Name.Literal)] = true
	}
	// Имя, объявленное хоть в одной процедуре модуля, не считается неизвестным:
	// такое чтение — это утечка области видимости, о ней сообщает
	// lintCrossScopeReads. Две проверки на одну строку только запутают.
	owned := map[string]bool{}
	for _, pr := range lp.prog.Procedures {
		ls := map[string]bool{}
		for _, p := range pr.Params {
			ls[strings.ToLower(p.Literal)] = true
		}
		collectDeclaredAndAssigned(pr.Body, ls)
		for name := range ls {
			owned[name] = true
		}
	}

	var issues []Issue
	for _, pr := range lp.prog.Procedures {
		bases := map[string]token.Token{}
		for _, def := range pr.Defaults {
			collectMemberBaseTokensExpr(def, bases)
		}
		collectMemberBaseTokensStmts(pr.Body, bases)
		names := make([]string, 0, len(bases))
		for name := range bases {
			names = append(names, name)
		}
		sort.Strings(names)
		inTPHandler := lp.tpContextProcs[strings.ToLower(pr.Name.Literal)]
		for _, name := range names {
			if owned[name] || moduleVars[name] || procNames[name] || commonDSLGlobals[name] || globals[name] ||
				lp.testContext && testDSLGlobals[name] ||
				inTPHandler && formTPContextGlobals[name] {
				continue
			}
			tok := bases[name]
			issues = append(issues, Issue{
				File:   sourceLabelForToken(lp.label, tok),
				Object: lp.object,
				Kind:   lp.kind,
				Code:   "dsl.unknown-global-member",
				Line:   tok.Line,
				Column: tok.Col,
				Message: fmt.Sprintf("обращение к полю %q: такого имени нет ни среди переменных, ни среди модулей и глобальных объектов — выражение молча вычислится в Неопределено",
					tok.Literal),
				SuggestedFix: "Проверьте написание имени; если это объект платформы — убедитесь, что он доступен в этом виде модуля, иначе получите «<nil>» в строке вместо значения.",
			})
		}
	}
	return issues
}

func crossScopeReadIssue(lp lintProgram, tok token.Token) Issue {
	return Issue{
		File:         sourceLabelForToken(lp.label, tok),
		Object:       lp.object,
		Kind:         lp.kind,
		Code:         "dsl.cross-scope-read",
		Line:         tok.Line,
		Column:       tok.Col,
		Message:      fmt.Sprintf("переменная %q не объявлена в этой процедуре и является локальной другой процедуры модуля — чтение работает лишь из-за утечки области видимости вызова", tok.Literal),
		SuggestedFix: "Передайте значение параметром/результатом функции или объявите переменную локально; не полагайтесь на видимость переменных вызывающей процедуры.",
	}
}

// collectDeclaredAndAssigned собирает имена, «принадлежащие» процедуре: Перем,
// переменные циклов и цели простых присваиваний (Ident = ...).
func collectDeclaredAndAssigned(stmts []ast.Stmt, out map[string]bool) {
	for _, stmt := range stmts {
		switch v := stmt.(type) {
		case *ast.VarDecl:
			for _, tok := range v.Names {
				out[strings.ToLower(tok.Literal)] = true
			}
		case *ast.AssignStmt:
			if id, ok := v.Target.(*ast.Ident); ok {
				out[strings.ToLower(id.Tok.Literal)] = true
			}
		case *ast.IfStmt:
			collectDeclaredAndAssigned(v.Then, out)
			for _, ei := range v.ElseIfs {
				collectDeclaredAndAssigned(ei.Body, out)
			}
			collectDeclaredAndAssigned(v.Else, out)
		case *ast.ForEachStmt:
			out[strings.ToLower(v.Var.Literal)] = true
			collectDeclaredAndAssigned(v.Body, out)
		case *ast.NumericForStmt:
			out[strings.ToLower(v.Var.Literal)] = true
			collectDeclaredAndAssigned(v.Body, out)
		case *ast.WhileStmt:
			collectDeclaredAndAssigned(v.Body, out)
		case *ast.TryStmt:
			collectDeclaredAndAssigned(v.Try, out)
			collectDeclaredAndAssigned(v.Except, out)
		}
	}
}

// collectReadIdentTokensStmts/Expr — как collectDSLReadsStmts, но сохраняет токен
// первого чтения каждого имени (для точной локации предупреждения). Цель
// присваивания Ident не считается чтением; callee прямого вызова — тоже.
// collectMemberBaseTokens* собирают идентификаторы, стоящие ОСНОВАНИЕМ обращения
// к полю: в «Символы.ПС» это «Символы». Присваивание вида «Х.Поле = …» тоже
// считается: чтобы записать поле, объект всё равно нужно получить.
func collectMemberBaseTokensExpr(expr ast.Expr, out map[string]token.Token) {
	if expr == nil {
		return
	}
	if member, ok := expr.(*ast.MemberExpr); ok {
		if ident, isIdent := member.Object.(*ast.Ident); isIdent {
			if k := strings.ToLower(ident.Tok.Literal); k != "" {
				if _, seen := out[k]; !seen {
					out[k] = ident.Tok
				}
			}
		}
	}
	switch v := expr.(type) {
	case *ast.CallExpr:
		collectMemberBaseTokensExpr(v.Callee, out)
		for _, arg := range v.Args {
			collectMemberBaseTokensExpr(arg, out)
		}
	case *ast.MemberExpr:
		collectMemberBaseTokensExpr(v.Object, out)
	case *ast.BinaryExpr:
		collectMemberBaseTokensExpr(v.Left, out)
		collectMemberBaseTokensExpr(v.Right, out)
	case *ast.UnaryExpr:
		collectMemberBaseTokensExpr(v.Operand, out)
	case *ast.NewExpr:
		for _, arg := range v.Args {
			collectMemberBaseTokensExpr(arg, out)
		}
	case *ast.ArrayLit:
		for _, elem := range v.Elements {
			collectMemberBaseTokensExpr(elem, out)
		}
	case *ast.IndexExpr:
		collectMemberBaseTokensExpr(v.Object, out)
		collectMemberBaseTokensExpr(v.Index, out)
	case *ast.TernaryExpr:
		collectMemberBaseTokensExpr(v.Cond, out)
		collectMemberBaseTokensExpr(v.True, out)
		collectMemberBaseTokensExpr(v.False, out)
	}
}

func collectMemberBaseTokensStmts(stmts []ast.Stmt, out map[string]token.Token) {
	for _, stmt := range stmts {
		switch v := stmt.(type) {
		case *ast.ExprStmt:
			collectMemberBaseTokensExpr(v.X, out)
		case *ast.AssignStmt:
			collectMemberBaseTokensExpr(v.Target, out)
			collectMemberBaseTokensExpr(v.Value, out)
		case *ast.ReturnStmt:
			collectMemberBaseTokensExpr(v.Value, out)
		case *ast.IfStmt:
			collectMemberBaseTokensExpr(v.Cond, out)
			collectMemberBaseTokensStmts(v.Then, out)
			for _, ei := range v.ElseIfs {
				collectMemberBaseTokensExpr(ei.Cond, out)
				collectMemberBaseTokensStmts(ei.Body, out)
			}
			collectMemberBaseTokensStmts(v.Else, out)
		case *ast.ForEachStmt:
			collectMemberBaseTokensExpr(v.Collection, out)
			collectMemberBaseTokensStmts(v.Body, out)
		case *ast.NumericForStmt:
			collectMemberBaseTokensExpr(v.Start, out)
			collectMemberBaseTokensExpr(v.End, out)
			collectMemberBaseTokensStmts(v.Body, out)
		case *ast.WhileStmt:
			collectMemberBaseTokensExpr(v.Cond, out)
			collectMemberBaseTokensStmts(v.Body, out)
		case *ast.TryStmt:
			collectMemberBaseTokensStmts(v.Try, out)
			collectMemberBaseTokensStmts(v.Except, out)
		}
	}
}

func collectReadIdentTokensStmts(stmts []ast.Stmt, out map[string]token.Token) {
	for _, stmt := range stmts {
		switch v := stmt.(type) {
		case *ast.ExprStmt:
			collectReadIdentTokensExpr(v.X, out)
		case *ast.AssignStmt:
			if v.Op == token.ASSIGN {
				collectReadIdentTokensTarget(v.Target, out)
			} else {
				collectReadIdentTokensExpr(v.Target, out)
			}
			collectReadIdentTokensExpr(v.Value, out)
		case *ast.ReturnStmt:
			collectReadIdentTokensExpr(v.Value, out)
		case *ast.IfStmt:
			collectReadIdentTokensExpr(v.Cond, out)
			collectReadIdentTokensStmts(v.Then, out)
			for _, ei := range v.ElseIfs {
				collectReadIdentTokensExpr(ei.Cond, out)
				collectReadIdentTokensStmts(ei.Body, out)
			}
			collectReadIdentTokensStmts(v.Else, out)
		case *ast.ForEachStmt:
			collectReadIdentTokensExpr(v.Collection, out)
			collectReadIdentTokensStmts(v.Body, out)
		case *ast.NumericForStmt:
			collectReadIdentTokensExpr(v.Start, out)
			collectReadIdentTokensExpr(v.End, out)
			collectReadIdentTokensStmts(v.Body, out)
		case *ast.WhileStmt:
			collectReadIdentTokensExpr(v.Cond, out)
			collectReadIdentTokensStmts(v.Body, out)
		case *ast.TryStmt:
			collectReadIdentTokensStmts(v.Try, out)
			collectReadIdentTokensStmts(v.Except, out)
		}
	}
}

func collectReadIdentTokensTarget(expr ast.Expr, out map[string]token.Token) {
	switch v := expr.(type) {
	case *ast.Ident:
		return
	case *ast.MemberExpr:
		collectReadIdentTokensExpr(v.Object, out)
	case *ast.IndexExpr:
		collectReadIdentTokensExpr(v.Object, out)
		collectReadIdentTokensExpr(v.Index, out)
	default:
		collectReadIdentTokensExpr(expr, out)
	}
}

func collectReadIdentTokensExpr(expr ast.Expr, out map[string]token.Token) {
	if expr == nil {
		return
	}
	switch v := expr.(type) {
	case *ast.Ident:
		if k := strings.ToLower(v.Tok.Literal); k != "" {
			if _, ok := out[k]; !ok {
				out[k] = v.Tok
			}
		}
	case *ast.CallExpr:
		if _, ok := v.Callee.(*ast.Ident); !ok {
			collectReadIdentTokensExpr(v.Callee, out)
		}
		for _, arg := range v.Args {
			collectReadIdentTokensExpr(arg, out)
		}
	case *ast.MemberExpr:
		collectReadIdentTokensExpr(v.Object, out)
	case *ast.BinaryExpr:
		collectReadIdentTokensExpr(v.Left, out)
		collectReadIdentTokensExpr(v.Right, out)
	case *ast.UnaryExpr:
		collectReadIdentTokensExpr(v.Operand, out)
	case *ast.NewExpr:
		for _, arg := range v.Args {
			collectReadIdentTokensExpr(arg, out)
		}
	case *ast.ArrayLit:
		for _, elem := range v.Elements {
			collectReadIdentTokensExpr(elem, out)
		}
	case *ast.IndexExpr:
		collectReadIdentTokensExpr(v.Object, out)
		collectReadIdentTokensExpr(v.Index, out)
	case *ast.TernaryExpr:
		collectReadIdentTokensExpr(v.Cond, out)
		collectReadIdentTokensExpr(v.True, out)
		collectReadIdentTokensExpr(v.False, out)
	}
}

func collectLintPrograms(dir string, proj *project.Project) []lintProgram {
	var out []lintProgram
	add := func(object, kind string, prog *ast.Program, roots map[string]bool, rootAll bool) {
		if prog == nil {
			return
		}
		out = append(out, lintProgram{
			label:   programLabel(dir, prog),
			object:  object,
			kind:    kind,
			prog:    prog,
			roots:   roots,
			rootAll: rootAll,
		})
	}

	entities := map[string]*metadata.Entity{}
	for _, e := range proj.Entities {
		entities[strings.ToLower(e.Name)] = e
	}
	processors := map[string]bool{}
	testProcessors := map[string]bool{}
	for _, p := range proj.Processors {
		low := strings.ToLower(p.Name)
		processors[low] = true
		testProcessors[low] = p.IsTest()
	}
	reportChartProcs := map[string]string{}
	for _, r := range proj.Reports {
		reportChartProcs[strings.ToLower(r.Name)] = r.ChartProc
	}

	for name, prog := range proj.Programs {
		low := strings.ToLower(name)
		switch {
		case processors[low]:
			add(name, "DSL обработка", prog, rootNames("Выполнить"), false)
			out[len(out)-1].testContext = testProcessors[low]
		case reportChartProcs[low] != "":
			add(name, "DSL отчёт", prog, rootNames(reportChartProcs[low]), false)
		case entities[low] != nil:
			add(name, "DSL объект", prog, rootNames(
				"OnWrite", "ПриЗаписи",
				"OnPost", "ОбработкаПроведения",
				"OnUnpost", "ОбработкаУдаленияПроведения",
				"OnFill", "ОбработкаЗаполнения",
				"OnCreate", "ПриСозданииНового",
				"Печать", "Print",
			), false)
		default:
			add(name, "DSL модуль", prog, nil, false)
		}
	}
	for name, prog := range proj.ManagerPrograms {
		add(name, "DSL менеджер", prog, nil, true)
	}

	serviceRoots := map[string]map[string]bool{}
	for _, svc := range proj.HTTPServices {
		roots := serviceRoots[strings.ToLower(svc.Name)]
		if roots == nil {
			roots = map[string]bool{}
			serviceRoots[strings.ToLower(svc.Name)] = roots
		}
		for _, tmpl := range svc.Templates {
			for _, handler := range tmpl.Methods {
				if strings.TrimSpace(handler) != "" {
					roots[strings.ToLower(handler)] = true
				}
			}
		}
	}
	for name, prog := range proj.ServicePrograms {
		add(name, "DSL HTTP-сервис", prog, serviceRoots[strings.ToLower(name)], false)
	}

	for name, prog := range proj.PagePrograms {
		add(name, "DSL страница", prog, rootNames("ПриФормировании"), false)
	}
	for name, prog := range proj.Modules {
		add(name, "DSL общий модуль", prog, nil, false)
	}

	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			prog, _ := form.ProgramAST.(*ast.Program)
			if prog == nil {
				continue
			}
			roots := map[string]bool{}
			collectFormHandlerRoots(form, roots)
			formName := form.Name
			if formName == "" {
				formName = ent.Name
			}
			add(ent.Name+"/"+formName, "DSL форма", prog, roots, false)
			// Контекст табличной части знает только её обработчик, поэтому
			// список процедур прикладывается к уже добавленной программе, а не
			// расширяет общий словарь глобалов.
			out[len(out)-1].tpContextProcs = collectTablePartHandlerProcs(form)
		}
	}
	return out
}

// collectTablePartHandlerProcs — процедуры, привязанные к событию табличной
// части или любого её потомка (колонки, кнопки подбора). Именно им рантайм
// собирает контекст строки; какой конкретно набор имён доедет, зависит от
// события и от того, что прислал клиент, поэтому разбирать это здесь не стоит:
// проверка отвечает на вопрос «существует ли такое имя вообще», а не «заполнено
// ли оно сейчас».
func collectTablePartHandlerProcs(form *metadata.FormModule) map[string]bool {
	if form == nil {
		return nil
	}
	procs := map[string]bool{}
	var walk func(elements []*metadata.FormElement, insideTablePart bool)
	walk = func(elements []*metadata.FormElement, insideTablePart bool) {
		for _, el := range elements {
			if el == nil {
				continue
			}
			inside := insideTablePart || el.Kind == metadata.FormElementTablePart
			if inside {
				for _, handler := range el.Handlers {
					addRoot(procs, handler)
				}
			}
			walk(el.Children, inside)
		}
	}
	walk(form.Elements, false)
	if len(procs) == 0 {
		return nil
	}
	return procs
}

func collectFormHandlerRoots(form *metadata.FormModule, roots map[string]bool) {
	for _, handler := range form.Handlers {
		addRoot(roots, handler)
	}
	var walkElements func([]*metadata.FormElement)
	walkElements = func(elements []*metadata.FormElement) {
		for _, el := range elements {
			for _, handler := range el.Handlers {
				addRoot(roots, handler)
			}
			walkElements(el.Children)
		}
	}
	walkElements(form.Elements)
	for _, cmd := range form.Commands {
		addRoot(roots, cmd.Action)
	}
}

func addRoot(roots map[string]bool, name string) {
	if strings.TrimSpace(name) != "" {
		roots[strings.ToLower(name)] = true
	}
}

func rootNames(names ...string) map[string]bool {
	roots := make(map[string]bool, len(names))
	for _, name := range names {
		addRoot(roots, name)
	}
	return roots
}

func lintUnusedVars(lp lintProgram) []Issue {
	if lp.prog == nil {
		return nil
	}
	var issues []Issue
	moduleVars := collectModuleVars(lp.prog)
	if len(moduleVars) > 0 {
		reads := map[string]int{}
		for _, pr := range lp.prog.Procedures {
			for _, def := range pr.Defaults {
				collectDSLReadsExpr(def, reads)
			}
			collectDSLReadsStmts(pr.Body, reads)
		}
		collectDSLReadsStmts(lp.prog.Body, reads)
		for _, decl := range lp.prog.ModuleVars {
			if decl.Exported {
				continue
			}
			for _, tok := range decl.Names {
				if reads[strings.ToLower(tok.Literal)] == 0 {
					issues = append(issues, unusedVarIssue(lp, tok, "переменная модуля"))
				}
			}
		}
	}

	moduleVarTokens := map[string]bool{}
	for _, decl := range lp.prog.ModuleVars {
		for _, tok := range decl.Names {
			moduleVarTokens[tokenKey(tok)] = true
		}
	}
	for _, pr := range lp.prog.Procedures {
		decls := map[string][]token.Token{}
		collectLocalVarDecls(pr.Body, decls, moduleVarTokens)
		if len(decls) == 0 {
			continue
		}
		reads := map[string]int{}
		for _, def := range pr.Defaults {
			collectDSLReadsExpr(def, reads)
		}
		collectDSLReadsStmts(pr.Body, reads)
		names := make([]string, 0, len(decls))
		for name := range decls {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if reads[name] > 0 {
				continue
			}
			for _, tok := range decls[name] {
				issues = append(issues, unusedVarIssue(lp, tok, "локальная переменная"))
			}
		}
	}
	return issues
}

func collectModuleVars(prog *ast.Program) map[string]token.Token {
	vars := map[string]token.Token{}
	for _, decl := range prog.ModuleVars {
		for _, tok := range decl.Names {
			vars[strings.ToLower(tok.Literal)] = tok
		}
	}
	return vars
}

func collectLocalVarDecls(stmts []ast.Stmt, out map[string][]token.Token, skip map[string]bool) {
	for _, stmt := range stmts {
		switch v := stmt.(type) {
		case *ast.VarDecl:
			for _, tok := range v.Names {
				if skip[tokenKey(tok)] {
					continue
				}
				out[strings.ToLower(tok.Literal)] = append(out[strings.ToLower(tok.Literal)], tok)
			}
		case *ast.IfStmt:
			collectLocalVarDecls(v.Then, out, skip)
			for _, ei := range v.ElseIfs {
				collectLocalVarDecls(ei.Body, out, skip)
			}
			collectLocalVarDecls(v.Else, out, skip)
		case *ast.ForEachStmt:
			collectLocalVarDecls(v.Body, out, skip)
		case *ast.NumericForStmt:
			collectLocalVarDecls(v.Body, out, skip)
		case *ast.TryStmt:
			collectLocalVarDecls(v.Try, out, skip)
			collectLocalVarDecls(v.Except, out, skip)
		}
	}
}

func unusedVarIssue(lp lintProgram, tok token.Token, role string) Issue {
	return Issue{
		File:         sourceLabelForToken(lp.label, tok),
		Object:       lp.object,
		Kind:         lp.kind,
		Code:         "dsl.unused-var",
		Line:         tok.Line,
		Column:       tok.Col,
		Message:      fmt.Sprintf("%s %q объявлена, но не читается", role, tok.Literal),
		SuggestedFix: "Удалите объявление или используйте переменную в коде.",
	}
}

func lintDeadProcedures(programs []lintProgram) []Issue {
	type procNode struct {
		lp    lintProgram
		proc  *ast.ProcedureDecl
		root  bool
		edges []int
	}
	var nodes []procNode
	byName := map[string][]int{}
	for _, lp := range programs {
		for _, pr := range lp.prog.Procedures {
			root := lp.rootAll || pr.Export || lp.roots[strings.ToLower(pr.Name.Literal)]
			nodes = append(nodes, procNode{lp: lp, proc: pr, root: root})
			idx := len(nodes) - 1
			byName[strings.ToLower(pr.Name.Literal)] = append(byName[strings.ToLower(pr.Name.Literal)], idx)
		}
	}
	for i := range nodes {
		calls := map[string]bool{}
		for _, def := range nodes[i].proc.Defaults {
			collectDSLCallNamesExpr(def, calls)
		}
		collectDSLCallNamesStmts(nodes[i].proc.Body, calls)
		for name := range calls {
			nodes[i].edges = append(nodes[i].edges, byName[name]...)
		}
	}
	reachable := make([]bool, len(nodes))
	var visit func(int)
	visit = func(i int) {
		if i < 0 || i >= len(nodes) || reachable[i] {
			return
		}
		reachable[i] = true
		for _, j := range nodes[i].edges {
			visit(j)
		}
	}
	for i := range nodes {
		if nodes[i].root {
			visit(i)
		}
	}
	var issues []Issue
	for i, n := range nodes {
		if reachable[i] {
			continue
		}
		tok := n.proc.Name
		issues = append(issues, Issue{
			File:         sourceLabelForToken(n.lp.label, tok),
			Object:       n.lp.object,
			Kind:         n.lp.kind,
			Code:         "dsl.dead-procedure",
			Line:         tok.Line,
			Column:       tok.Col,
			Message:      fmt.Sprintf("процедура %q не достижима из известных точек входа", tok.Literal),
			SuggestedFix: "Удалите процедуру, вызовите её из рабочей точки входа или пометьте Экспорт, если она вызывается извне.",
		})
	}
	return issues
}

func collectDSLReadsStmts(stmts []ast.Stmt, reads map[string]int) {
	for _, stmt := range stmts {
		switch v := stmt.(type) {
		case *ast.ExprStmt:
			collectDSLReadsExpr(v.X, reads)
		case *ast.AssignStmt:
			if v.Op == token.ASSIGN {
				collectDSLTargetReads(v.Target, reads)
			} else {
				collectDSLReadsExpr(v.Target, reads)
			}
			collectDSLReadsExpr(v.Value, reads)
		case *ast.ReturnStmt:
			collectDSLReadsExpr(v.Value, reads)
		case *ast.IfStmt:
			collectDSLReadsExpr(v.Cond, reads)
			collectDSLReadsStmts(v.Then, reads)
			for _, ei := range v.ElseIfs {
				collectDSLReadsExpr(ei.Cond, reads)
				collectDSLReadsStmts(ei.Body, reads)
			}
			collectDSLReadsStmts(v.Else, reads)
		case *ast.ForEachStmt:
			collectDSLReadsExpr(v.Collection, reads)
			collectDSLReadsStmts(v.Body, reads)
		case *ast.NumericForStmt:
			collectDSLReadsExpr(v.Start, reads)
			collectDSLReadsExpr(v.End, reads)
			collectDSLReadsStmts(v.Body, reads)
		case *ast.WhileStmt:
			collectDSLReadsExpr(v.Cond, reads)
			collectDSLReadsStmts(v.Body, reads)
		case *ast.TryStmt:
			collectDSLReadsStmts(v.Try, reads)
			collectDSLReadsStmts(v.Except, reads)
		}
	}
}

func collectDSLTargetReads(expr ast.Expr, reads map[string]int) {
	switch v := expr.(type) {
	case *ast.Ident:
		return
	case *ast.MemberExpr:
		collectDSLReadsExpr(v.Object, reads)
	case *ast.IndexExpr:
		collectDSLReadsExpr(v.Object, reads)
		collectDSLReadsExpr(v.Index, reads)
	default:
		collectDSLReadsExpr(expr, reads)
	}
}

func collectDSLReadsExpr(expr ast.Expr, reads map[string]int) {
	if expr == nil {
		return
	}
	switch v := expr.(type) {
	case *ast.Ident:
		reads[strings.ToLower(v.Tok.Literal)]++
	case *ast.CallExpr:
		if _, ok := v.Callee.(*ast.Ident); !ok {
			collectDSLReadsExpr(v.Callee, reads)
		}
		for _, arg := range v.Args {
			collectDSLReadsExpr(arg, reads)
		}
	case *ast.MemberExpr:
		collectDSLReadsExpr(v.Object, reads)
	case *ast.BinaryExpr:
		collectDSLReadsExpr(v.Left, reads)
		collectDSLReadsExpr(v.Right, reads)
	case *ast.UnaryExpr:
		collectDSLReadsExpr(v.Operand, reads)
	case *ast.NewExpr:
		for _, arg := range v.Args {
			collectDSLReadsExpr(arg, reads)
		}
	case *ast.ArrayLit:
		for _, elem := range v.Elements {
			collectDSLReadsExpr(elem, reads)
		}
	case *ast.IndexExpr:
		collectDSLReadsExpr(v.Object, reads)
		collectDSLReadsExpr(v.Index, reads)
	case *ast.TernaryExpr:
		collectDSLReadsExpr(v.Cond, reads)
		collectDSLReadsExpr(v.True, reads)
		collectDSLReadsExpr(v.False, reads)
	}
}

func collectDSLCallNamesStmts(stmts []ast.Stmt, calls map[string]bool) {
	for _, stmt := range stmts {
		switch v := stmt.(type) {
		case *ast.ExprStmt:
			collectDSLCallNamesExpr(v.X, calls)
		case *ast.AssignStmt:
			collectDSLCallNamesExpr(v.Target, calls)
			collectDSLCallNamesExpr(v.Value, calls)
		case *ast.ReturnStmt:
			collectDSLCallNamesExpr(v.Value, calls)
		case *ast.IfStmt:
			collectDSLCallNamesExpr(v.Cond, calls)
			collectDSLCallNamesStmts(v.Then, calls)
			for _, ei := range v.ElseIfs {
				collectDSLCallNamesExpr(ei.Cond, calls)
				collectDSLCallNamesStmts(ei.Body, calls)
			}
			collectDSLCallNamesStmts(v.Else, calls)
		case *ast.ForEachStmt:
			collectDSLCallNamesExpr(v.Collection, calls)
			collectDSLCallNamesStmts(v.Body, calls)
		case *ast.NumericForStmt:
			collectDSLCallNamesExpr(v.Start, calls)
			collectDSLCallNamesExpr(v.End, calls)
			collectDSLCallNamesStmts(v.Body, calls)
		case *ast.WhileStmt:
			collectDSLCallNamesExpr(v.Cond, calls)
			collectDSLCallNamesStmts(v.Body, calls)
		case *ast.TryStmt:
			collectDSLCallNamesStmts(v.Try, calls)
			collectDSLCallNamesStmts(v.Except, calls)
		}
	}
}

func collectDSLCallNamesExpr(expr ast.Expr, calls map[string]bool) {
	if expr == nil {
		return
	}
	switch v := expr.(type) {
	case *ast.CallExpr:
		if ident, ok := v.Callee.(*ast.Ident); ok {
			calls[strings.ToLower(ident.Tok.Literal)] = true
		} else {
			collectDSLCallNamesExpr(v.Callee, calls)
		}
		for _, arg := range v.Args {
			collectDSLCallNamesExpr(arg, calls)
		}
	case *ast.MemberExpr:
		collectDSLCallNamesExpr(v.Object, calls)
	case *ast.BinaryExpr:
		collectDSLCallNamesExpr(v.Left, calls)
		collectDSLCallNamesExpr(v.Right, calls)
	case *ast.UnaryExpr:
		collectDSLCallNamesExpr(v.Operand, calls)
	case *ast.NewExpr:
		for _, arg := range v.Args {
			collectDSLCallNamesExpr(arg, calls)
		}
	case *ast.ArrayLit:
		for _, elem := range v.Elements {
			collectDSLCallNamesExpr(elem, calls)
		}
	case *ast.IndexExpr:
		collectDSLCallNamesExpr(v.Object, calls)
		collectDSLCallNamesExpr(v.Index, calls)
	case *ast.TernaryExpr:
		collectDSLCallNamesExpr(v.Cond, calls)
		collectDSLCallNamesExpr(v.True, calls)
		collectDSLCallNamesExpr(v.False, calls)
	}
}

func CheckLintRoles(dir string, proj *project.Project, roles []*auth.Role) []Issue {
	if len(roles) == 0 {
		return nil
	}
	coveredCatalogs := map[string]bool{}
	coveredDocuments := map[string]bool{}
	coveredRegisters := map[string]bool{}
	coveredInfoRegs := map[string]bool{}
	coveredReports := map[string]bool{}
	coveredProcessors := map[string]bool{}
	processorsOpen := false

	mark := func(dst map[string]bool, src map[string][]string) {
		for name, ops := range src {
			if len(ops) > 0 {
				dst[strings.ToLower(name)] = true
			}
		}
	}
	for _, role := range roles {
		mark(coveredCatalogs, role.Permissions.Catalogs)
		mark(coveredDocuments, role.Permissions.Documents)
		mark(coveredRegisters, role.Permissions.Registers)
		mark(coveredInfoRegs, role.Permissions.InfoRegs)
		mark(coveredReports, role.Permissions.Reports)
		if role.Permissions.Processors == nil {
			processorsOpen = true
		} else {
			mark(coveredProcessors, role.Permissions.Processors)
		}
	}

	var issues []Issue
	add := func(file, object, kind string) {
		issues = append(issues, Issue{
			File:         file,
			Object:       object,
			Kind:         kind,
			Code:         "rbac.object-without-role",
			Message:      fmt.Sprintf("%s %q не получает прав ни в одной роли", kind, object),
			SuggestedFix: "Добавьте объект в roles/*.yaml или удалите его, если он больше не используется.",
		})
	}
	for _, ent := range proj.Entities {
		if ent.Kind == metadata.KindCatalog {
			if !coveredCatalogs[strings.ToLower(ent.Name)] {
				add("catalogs/"+ent.Name+".yaml", ent.Name, "Справочник")
			}
			continue
		}
		if !coveredDocuments[strings.ToLower(ent.Name)] {
			add("documents/"+ent.Name+".yaml", ent.Name, "Документ")
		}
	}
	for _, reg := range proj.Registers {
		if !coveredRegisters[strings.ToLower(reg.Name)] {
			add("registers/"+reg.Name+".yaml", reg.Name, "Регистр")
		}
	}
	for _, ir := range proj.InfoRegisters {
		if !coveredInfoRegs[strings.ToLower(ir.Name)] {
			add("inforegs/"+ir.Name+".yaml", ir.Name, "Регистр сведений")
		}
	}
	for _, rep := range proj.Reports {
		if !coveredReports[strings.ToLower(rep.Name)] {
			add("reports/"+rep.Name+".yaml", rep.Name, "Отчёт")
		}
	}
	if !processorsOpen {
		for _, proc := range proj.Processors {
			// Тест-обработки (kind: test) не показываются пользователю и не
			// требуют прав в ролях — их гоняет только раннер `onebase test`.
			if proc.IsTest() {
				continue
			}
			if !coveredProcessors[strings.ToLower(proc.Name)] {
				add("processors/"+proc.Name+".yaml", proc.Name, "Обработка")
			}
		}
	}
	issues = append(issues, checkLintUnknownRoleRefs(proj, roles)...)
	issues = append(issues, CheckLintRowAccess(dir, proj, roles)...)
	issues = append(issues, CheckLintFieldAccess(dir, proj, roles)...)
	return issues
}

// checkLintUnknownRoleRefs ловит опечатки в whitelist-полях `roles:` подсистем
// и страниц: роль, которой нет в roles/*.yaml, молча спрятала бы объект у всех
// не-админов. Мягкий линт, а не жёсткая ошибка cross_refs, потому что роли
// могут существовать и только в БД (созданы через админку) — тогда yaml-набор
// заведомо неполный.
func checkLintUnknownRoleRefs(proj *project.Project, roles []*auth.Role) []Issue {
	known := map[string]bool{}
	for _, role := range roles {
		known[strings.ToLower(role.Name)] = true
	}
	var issues []Issue
	check := func(file, object, kind string, refs []string) {
		for _, name := range refs {
			if strings.TrimSpace(name) == "" || known[strings.ToLower(strings.TrimSpace(name))] {
				continue
			}
			issues = append(issues, Issue{
				File:         file,
				Object:       object,
				Kind:         kind,
				Code:         "rbac.unknown-role",
				Message:      fmt.Sprintf("%s %q: роль %q из списка roles не найдена в roles/*.yaml", kind, object, name),
				SuggestedFix: "Исправьте опечатку или создайте роль — иначе объект скрыт у всех, кроме админа (если роль есть только в БД, предупреждение можно игнорировать).",
			})
		}
	}
	for _, s := range proj.Subsystems {
		check("subsystems/"+s.Name+".yaml", s.Name, "Подсистема", s.Roles)
	}
	for _, pg := range proj.Pages {
		check("pages/"+pg.Name+".yaml", pg.Name, "Страница", pg.Roles)
	}
	return issues
}

type rowAccessLintTarget struct {
	name       string
	meta       *metadata.Entity
	objectKind string
}

func CheckLintRowAccess(dir string, proj *project.Project, roles []*auth.Role) []Issue {
	if proj == nil || len(roles) == 0 {
		return nil
	}
	targets := rowAccessLintTargets(proj)
	roleFiles := roleFileLabels(dir)
	var issues []Issue
	for _, role := range roles {
		if role == nil || role.Permissions.RowAccess.IsZero() {
			continue
		}
		file := roleFiles[strings.ToLower(role.Name)]
		if file == "" {
			file = "roles"
		}
		addSection := func(kind, section string, policies map[string]auth.RowPolicies) {
			for object, ops := range policies {
				target, ok := targets[rowAccessTargetKey(kind, object)]
				if !ok {
					issues = append(issues, Issue{
						File:         file,
						Object:       role.Name,
						Kind:         "Роль",
						Code:         "rls.unknown-object",
						Message:      fmt.Sprintf("row_access.%s.%s в роли %q ссылается на несуществующий объект", section, object, role.Name),
						SuggestedFix: "Исправьте имя объекта в permissions.row_access или удалите устаревшую policy.",
					})
					continue
				}
				for op, raw := range ops {
					if !auth.PermissionHas(role.Permissions, kind, object, op) {
						issues = append(issues, Issue{
							File:         file,
							Object:       role.Name,
							Kind:         "Роль",
							Code:         "rls.policy-without-permission",
							Message:      fmt.Sprintf("row_access.%s.%s.%s в роли %q не применяется: роль не даёт object-level право %q на %s %q", section, object, op, role.Name, op, target.objectKind, target.name),
							SuggestedFix: fmt.Sprintf("Добавьте `%s` в permissions.%s.%s или удалите эту row_access policy.", op, section, object),
						})
					}
					if err := validateRoleRowPolicy(ops, op, raw, target.meta, projEntityLookup(proj)); err != nil {
						issues = append(issues, Issue{
							File:         file,
							Object:       role.Name,
							Kind:         "Роль",
							Code:         "rls.invalid-policy",
							Message:      fmt.Sprintf("row_access.%s.%s.%s в роли %q невалидна: %v", section, object, op, role.Name, err),
							SuggestedFix: "Исправьте field/op/value/same_as в policy; `onebase check --lint` проверяет её тем же компилятором, что runtime.",
						})
					}
				}
			}
		}
		addSection("catalog", "catalogs", role.Permissions.RowAccess.Catalogs)
		addSection("document", "documents", role.Permissions.RowAccess.Documents)
		addSection("register", "registers", role.Permissions.RowAccess.Registers)
		addSection("inforeg", "inforegs", role.Permissions.RowAccess.InfoRegs)
	}
	return issues
}

// CheckLintFieldAccess validates field-level masking policies (план 88): unknown
// object/field, unknown or type-inapplicable strategy, a policy without the
// object-level `read` it depends on, and object-level `disclose` without `read`.
func CheckLintFieldAccess(dir string, proj *project.Project, roles []*auth.Role) []Issue {
	if proj == nil || len(roles) == 0 {
		return nil
	}
	targets := rowAccessLintTargets(proj)
	roleFiles := roleFileLabels(dir)
	var issues []Issue
	for _, role := range roles {
		if role == nil {
			continue
		}
		file := roleFiles[strings.ToLower(role.Name)]
		if file == "" {
			file = "roles"
		}
		issues = append(issues, lintDiscloseWithoutRead(role, file)...)
		if role.Permissions.FieldAccess.IsZero() {
			continue
		}
		addSection := func(kind, section string, policies map[string]auth.FieldPolicies) {
			for object, fields := range policies {
				target, ok := targets[rowAccessTargetKey(kind, object)]
				if !ok {
					issues = append(issues, Issue{
						File:         file,
						Object:       role.Name,
						Kind:         "Роль",
						Code:         "mask.unknown-object",
						Message:      fmt.Sprintf("field_access.%s.%s в роли %q ссылается на несуществующий объект", section, object, role.Name),
						SuggestedFix: "Исправьте имя объекта в permissions.field_access или удалите устаревшую policy.",
					})
					continue
				}
				if !auth.PermissionHas(role.Permissions, kind, object, "read") {
					issues = append(issues, Issue{
						File:         file,
						Object:       role.Name,
						Kind:         "Роль",
						Code:         "mask.policy-without-permission",
						Message:      fmt.Sprintf("field_access.%s.%s в роли %q не применяется: роль не даёт object-level право read на %s %q", section, object, role.Name, target.objectKind, target.name),
						SuggestedFix: fmt.Sprintf("Добавьте `read` в permissions.%s.%s или удалите эту field_access policy.", section, object),
					})
				}
				for field, pol := range fields {
					if err := access.ValidateFieldPolicy(field, pol, target.meta); err != nil {
						issues = append(issues, Issue{
							File:         file,
							Object:       role.Name,
							Kind:         "Роль",
							Code:         "mask.invalid-policy",
							Message:      fmt.Sprintf("field_access.%s.%s.%s в роли %q невалидна: %v", section, object, field, role.Name, err),
							SuggestedFix: "Стратегии: full | mask_tail (+keep) | mask_city | mask_all | hide; поле должно существовать в объекте.",
						})
					}
				}
			}
		}
		addSection("catalog", "catalogs", role.Permissions.FieldAccess.Catalogs)
		addSection("document", "documents", role.Permissions.FieldAccess.Documents)
		addSection("register", "registers", role.Permissions.FieldAccess.Registers)
		addSection("inforeg", "inforegs", role.Permissions.FieldAccess.InfoRegs)
	}
	return issues
}

// lintDiscloseWithoutRead flags an object-level `disclose` right granted without
// `read`: раскрывать поле, которое роль и так не читает, бессмысленно.
func lintDiscloseWithoutRead(role *auth.Role, file string) []Issue {
	var issues []Issue
	check := func(kind, section string, m map[string][]string) {
		for object := range m {
			if !auth.PermissionHas(role.Permissions, kind, object, "disclose") {
				continue
			}
			if auth.PermissionHas(role.Permissions, kind, object, "read") {
				continue
			}
			issues = append(issues, Issue{
				File:         file,
				Object:       role.Name,
				Kind:         "Роль",
				Code:         "mask.disclose-without-read",
				Message:      fmt.Sprintf("permissions.%s.%s в роли %q даёт `disclose` без `read` — раскрывать нечего", section, object, role.Name),
				SuggestedFix: "Добавьте `read` к объекту или уберите `disclose`.",
			})
		}
	}
	check("catalog", "catalogs", role.Permissions.Catalogs)
	check("document", "documents", role.Permissions.Documents)
	check("register", "registers", role.Permissions.Registers)
	check("inforeg", "inforegs", role.Permissions.InfoRegs)
	return issues
}

func rowAccessLintTargets(proj *project.Project) map[string]rowAccessLintTarget {
	out := map[string]rowAccessLintTarget{}
	add := func(kind, name string, meta *metadata.Entity, objectKind string) {
		out[rowAccessTargetKey(kind, name)] = rowAccessLintTarget{
			name:       name,
			meta:       meta,
			objectKind: objectKind,
		}
	}
	for _, ent := range proj.Entities {
		if ent == nil {
			continue
		}
		if ent.Kind == metadata.KindCatalog {
			add("catalog", ent.Name, ent, "справочник")
		} else {
			add("document", ent.Name, ent, "документ")
		}
	}
	for _, reg := range proj.Registers {
		if reg != nil {
			add("register", reg.Name, storage.RegisterPredicateEntity(reg), "регистр")
		}
	}
	for _, ar := range proj.AccountRegisters {
		if ar != nil {
			add("register", ar.Name, storage.AccountRegisterPredicateEntity(ar), "регистр бухгалтерии")
		}
	}
	for _, ir := range proj.InfoRegisters {
		if ir != nil {
			add("inforeg", ir.Name, storage.InfoRegisterPredicateEntity(ir), "регистр сведений")
		}
	}
	return out
}

func rowAccessTargetKey(kind, name string) string {
	return strings.ToLower(strings.TrimSpace(kind)) + "\x00" + strings.ToLower(strings.TrimSpace(name))
}

func roleFileLabels(dir string) map[string]string {
	out := map[string]string{}
	if dir == "" {
		return out
	}
	root := filepath.Join(dir, "roles")
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
			continue
		}
		label := filepath.ToSlash(filepath.Join("roles", e.Name()))
		role, err := auth.LoadRoleFile(filepath.Join(root, e.Name()))
		if err != nil || role == nil || strings.TrimSpace(role.Name) == "" {
			continue
		}
		out[strings.ToLower(role.Name)] = label
	}
	return out
}

func validateRoleRowPolicy(policies auth.RowPolicies, op string, raw auth.RowPolicy, meta *metadata.Entity, lookup access.EntityLookup) error {
	if raw.SameAs != "" {
		if err := validateRowPolicySameAs(policies, op); err != nil {
			return err
		}
		resolved, _ := policies.Resolve(op)
		return access.ValidatePolicyWithLookup(resolved, meta, lookup)
	}
	return access.ValidatePolicyWithLookup(raw, meta, lookup)
}

type projectEntityLookup project.Project

func projEntityLookup(proj *project.Project) access.EntityLookup {
	if proj == nil {
		return nil
	}
	return (*projectEntityLookup)(proj)
}

func (p *projectEntityLookup) GetEntity(name string) *metadata.Entity {
	if p == nil {
		return nil
	}
	for _, ent := range p.Entities {
		if ent != nil && strings.EqualFold(ent.Name, name) {
			return ent
		}
	}
	return nil
}

func validateRowPolicySameAs(policies auth.RowPolicies, op string) error {
	seen := map[string]bool{}
	cur := strings.ToLower(strings.TrimSpace(op))
	for {
		if seen[cur] {
			return fmt.Errorf("same_as образует цикл на операции %q", cur)
		}
		seen[cur] = true
		p, ok := policies[cur]
		if !ok {
			return fmt.Errorf("same_as ссылается на отсутствующую операцию %q", cur)
		}
		if p.SameAs == "" {
			return nil
		}
		cur = strings.ToLower(strings.TrimSpace(p.SameAs))
	}
}

func CheckLintIndexes(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	var issues []Issue
	for _, ent := range proj.Entities {
		if ent == nil || len(ent.ListForm) == 0 {
			continue
		}
		for _, fieldName := range ent.ListForm {
			f := findLintEntityField(ent, fieldName)
			if f == nil || !lintFieldNeedsIndex(*f) || hasLeadingIndex(ent, f.Name) {
				continue
			}
			kindDir, kindName := "catalogs", "Справочник"
			if ent.Kind == metadata.KindDocument {
				kindDir, kindName = "documents", "Документ"
			}
			issues = append(issues, Issue{
				File:         kindDir + "/" + ent.Name + ".yaml",
				Object:       ent.Name,
				Kind:         kindName,
				Code:         "metadata.list-field-without-index",
				Message:      fmt.Sprintf("поле %q используется в list_form, но не покрыто ведущим полем indexes:", f.Name),
				SuggestedFix: fmt.Sprintf("Добавьте в %s/%s.yaml блок `indexes: - fields: [%s]` или составной индекс, где %s идёт первым.", kindDir, ent.Name, f.Name, f.Name),
			})
		}
	}
	return issues
}

func findLintEntityField(ent *metadata.Entity, name string) *metadata.Field {
	for i := range ent.Fields {
		if strings.EqualFold(ent.Fields[i].Name, name) {
			return &ent.Fields[i]
		}
	}
	return nil
}

func lintFieldNeedsIndex(f metadata.Field) bool {
	if f.RefEntity != "" {
		return true
	}
	switch f.Type {
	case metadata.FieldTypeString, metadata.FieldTypeDate, metadata.FieldTypeNumber, metadata.FieldTypeBool:
		return true
	default:
		return false
	}
}

func hasLeadingIndex(ent *metadata.Entity, fieldName string) bool {
	for _, idx := range ent.Indexes {
		if len(idx.Fields) > 0 && strings.EqualFold(idx.Fields[0], fieldName) {
			return true
		}
	}
	return false
}

func programLabel(dir string, prog *ast.Program) string {
	for _, pr := range prog.Procedures {
		if pr.Name.File != "" {
			return relLabel(dir, pr.Name.File)
		}
	}
	for _, decl := range prog.ModuleVars {
		for _, tok := range decl.Names {
			if tok.File != "" {
				return relLabel(dir, tok.File)
			}
		}
	}
	return ""
}

func relLabel(root, path string) string {
	if path == "" {
		return ""
	}
	if rel, err := filepath.Rel(root, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func sourceLabelForToken(fallback string, tok token.Token) string {
	if fallback != "" {
		return fallback
	}
	return filepath.ToSlash(tok.File)
}

func tokenKey(tok token.Token) string {
	return fmt.Sprintf("%s:%d:%d:%s", tok.File, tok.Line, tok.Col, strings.ToLower(tok.Literal))
}
