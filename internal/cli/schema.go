package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var schemaCmd = &cobra.Command{
	Use:   "schema [kind]",
	Short: "Вывести JSON Schema для YAML-конфигурации",
	Long: `Печатает JSON Schema для вида метаданных OneBase.
Без аргумента выводит объект со всеми схемами. Команда используется редакторами,
MCP-клиентами и ИИ-ассистентами как строгий машинный контракт YAML.`,
	Args:          cobra.MaximumNArgs(1),
	RunE:          runSchema,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	schemaCmd.Flags().String("kind", "", "вид объекта (catalog, document, register, widget, ...)")
	schemaCmd.Flags().Bool("list", false, "показать доступные виды схем")
	rootCmd.AddCommand(schemaCmd)
}

func runSchema(cmd *cobra.Command, args []string) error {
	if list, _ := cmd.Flags().GetBool("list"); list {
		outln(strings.Join(schemaKinds(), "\n"))
		return nil
	}
	kind, _ := cmd.Flags().GetString("kind")
	if kind == "" && len(args) > 0 {
		kind = args[0]
	}
	if kind != "" {
		s, ok := schemaByKind(kind)
		if !ok {
			return fmt.Errorf("неизвестный вид схемы %q\nдоступно:\n%s", kind, strings.Join(schemaKinds(), "\n"))
		}
		return encodeSchema(s)
	}
	all := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title":   "OneBase metadata schemas",
		"type":    "object",
		"$defs":   allSchemas(),
	}
	return encodeSchema(all)
}

func encodeSchema(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func schemaKinds() []string {
	out := make([]string, 0, len(schemaAliases))
	seen := map[string]bool{}
	for _, k := range schemaAliases {
		if !seen[k] {
			out = append(out, k)
			seen[k] = true
		}
	}
	sort.Strings(out)
	return out
}

func schemaByKind(kind string) (map[string]any, bool) {
	key := strings.ToLower(strings.TrimSpace(kind))
	key = strings.ReplaceAll(key, "_", "-")
	canon, ok := schemaAliases[key]
	if !ok {
		return nil, false
	}
	s, ok := allSchemas()[canon]
	return s, ok
}

var schemaAliases = map[string]string{
	"catalog": "entity", "справочник": "entity",
	"document": "entity", "документ": "entity",
	"entity":   "entity",
	"register": "register", "регистр": "register",
	"inforeg": "inforeg", "info-register": "inforeg", "регистр-сведений": "inforeg",
	"enum": "enum", "перечисление": "enum",
	"constants": "constants", "constant": "constants", "константы": "constants",
	"processor": "processor", "обработка": "processor",
	"report": "report", "отчёт": "report", "отчет": "report",
	"widget": "widget", "виджет": "widget",
	"form": "form", "форма": "form",
	"role": "role", "роль": "role",
	"page": "page", "страница": "page",
	"service": "service", "http-service": "service", "сервис": "service",
	"subsystem": "subsystem", "подсистема": "subsystem",
	"journal": "journal", "журнал": "journal",
	"scheduled": "scheduled", "job": "scheduled", "регламентное-задание": "scheduled",
	"accounts": "accounts", "chart-of-accounts": "accounts", "план-счетов": "accounts",
	"accountreg": "accountreg", "account-register": "accountreg", "регистр-бухгалтерии": "accountreg",
	"home-page": "home-page", "homepage": "home-page",
}

func allSchemas() map[string]map[string]any {
	field := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"name", "type"},
		"properties": map[string]any{
			"name":                stringSchema("Имя реквизита"),
			"title":               stringSchema("Синоним"),
			"label":               stringSchema("Алиас синонима"),
			"titles":              stringMapSchema(),
			"type":                stringSchema("string|number|date|bool|text|richtext|image|reference:<Объект>|enum:<Перечисление>|number(10,2)"),
			"allow_inline_create": map[string]any{"type": "boolean"},
		},
	}
	tablePart := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"name"},
		"properties": map[string]any{
			"name":   stringSchema("Имя табличной части"),
			"title":  stringSchema("Синоним"),
			"titles": stringMapSchema(),
			"fields": arrayOf(field),
		},
	}
	param := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"name", "type"},
		"properties": map[string]any{
			"name":    stringSchema("Имя параметра"),
			"type":    stringSchema("Тип параметра"),
			"label":   stringSchema("Подпись"),
			"options": arrayOf(stringSchema("Значение")),
		},
	}
	detailPanelTab := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"name", "fields"},
		"properties": map[string]any{
			"name":   stringSchema("Имя закладки"),
			"titles": stringMapSchema(),
			"fields": map[string]any{
				"type":     "array",
				"minItems": 1,
				"items":    stringSchema("Реквизит на закладке"),
			},
			"tableparts":  arrayOf(stringSchema("Табличная часть (зарезервировано для 118D)")),
			"attachments": map[string]any{"type": "boolean", "description": "Вложения (зарезервировано для 118D)"},
		},
	}
	detailPanel := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"description":          "Состав боковой панели деталей списка. Без блока — автокомпоновка: все реквизиты шапки, картинки и размеченный текст на своих закладках",
		"allOf": []any{
			map[string]any{"not": map[string]any{"required": []string{"fields", "tabs"}}},
		},
		"properties": map[string]any{
			"title": stringSchema("Реквизит-заголовок карточки; по умолчанию — представление записи"),
			"width": map[string]any{
				"description": "Ширина по умолчанию, px; 0 или отсутствие — 320",
				"anyOf": []any{
					map[string]any{"const": 0},
					map[string]any{"type": "integer", "minimum": 220, "maximum": 640},
				},
			},
			"fields": arrayOf(stringSchema("Реквизит панели (короткая форма без явных закладок)")),
			"tabs": map[string]any{
				"type":     "array",
				"minItems": 1,
				"items":    detailPanelTab,
			},
		},
	}

	stages := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"field", "order"},
		"description":          "Этапы объекта: порядок состояний и допустимые переходы на существующем реквизите-перечислении. Гейт стоит в слое хранения, поэтому его не обойти ни формой, ни обработкой, ни REST",
		"properties": map[string]any{
			"field": stringSchema("Реквизит типа enum:…, ведущий этапы"),
			"order": map[string]any{
				"type":        "array",
				"minItems":    1,
				"items":       stringSchema("Значение перечисления"),
				"description": "Порядок этапов; первый считается начальным — объект без заполненного этапа стоит на нём",
			},
			"transitions": map[string]any{
				"type":        "array",
				"description": "Допустимые переходы; всё, чего здесь нет, запрещено",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"from", "to"},
					"properties": map[string]any{
						"from": stringSchema("Этап, из которого разрешён переход"),
						"to": map[string]any{
							"type":     "array",
							"minItems": 1,
							"items":    stringSchema("Этап назначения"),
						},
					},
				},
			},
			"deadline_days": map[string]any{
				"type":                 "object",
				"description":          "Этап → сколько дней объект вправе на нём стоять; просрочка попадает в отчёт «где застряло»",
				"additionalProperties": map[string]any{"type": "integer", "minimum": 1},
			},
			"enforce": stringSchema("warn (умолчание: нарушение только в лог) | strict (недопустимый переход отвергается)"),
		},
	}

	return map[string]map[string]any{
		"entity": {
			"$schema":              "https://json-schema.org/draft/2020-12/schema",
			"title":                "OneBase catalog/document",
			"type":                 "object",
			"required":             []string{"name"},
			"additionalProperties": false,
			"properties": map[string]any{
				"name":                  stringSchema("Имя объекта"),
				"title":                 stringSchema("Синоним"),
				"description":           stringSchema("Описание объекта"),
				"titles":                stringMapSchema(),
				"fields":                arrayOf(field),
				"tableparts":            arrayOf(tablePart),
				"posting":               map[string]any{"type": "boolean"},
				"post_caption":          stringSchema("Подпись кнопки проведения (пусто → «Провести»)"),
				"post_and_close_hidden": map[string]any{"type": "boolean"},
				"hierarchical":          map[string]any{"type": "boolean"},
				"hierarchy_kind":        stringSchema("folders_and_items|items_only"),
				"based_on":              arrayOf(stringSchema("Имя исходного объекта")),
				"list_form":             arrayOf(stringSchema("Имя поля")),
				"item_form":             arrayOf(stringSchema("Имя поля")),
				"list_mode":             stringSchema("pages|feed"),
				"fulltext":              arrayOf(stringSchema("Реквизит, попадающий в полнотекстовый поиск (по умолчанию — все строковые)")),
				"search_fields":         arrayOf(stringSchema("Реквизит для поиска по строке в списке и подборе (по умолчанию — все строковые)")),
				"detail_panel":          detailPanel,
				"stages":                stages,
				"numerator": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"prefix":      stringSchema("Префикс номера/кода; поддерживает маски даты {YYYY}, {YY}, {MM}, {DD}"),
						"length":      map[string]any{"type": "integer", "minimum": 1},
						"period":      stringSchema("year|month|day|none (у справочника — none)"),
						"scope":       stringSchema("Поле области нумерации"),
						"base_prefix": map[string]any{"type": "boolean", "description": "Подставлять префикс этой базы (для обмена между базами)"},
						"unique":      map[string]any{"type": "boolean", "description": "Требовать уникальность кода/номера"},
					},
				},
				"predefined": arrayOf(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":   stringSchema("Имя предопределённого элемента"),
						"fields": map[string]any{"type": "object"},
					},
					"required": []string{"name"},
				}),
				"tile_view": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"image":    stringSchema("Поле картинки"),
						"title":    stringSchema("Поле заголовка"),
						"subtitle": stringSchema("Поле подзаголовка"),
						"fields":   arrayOf(stringSchema("Поле")),
					},
				},
			},
		},
		"register": fieldGroupSchema("OneBase accumulation register", field, []string{"dimensions", "resources", "attributes"}, map[string]any{"kind": stringSchema("balance|turnover")}),
		"inforeg": fieldGroupSchema("OneBase information register", field, []string{"dimensions", "resources"}, map[string]any{
			"periodic": map[string]any{"type": "boolean"},
			"recorder": map[string]any{"type": "boolean", "description": "регистр подчинён регистратору: строки формирует проведение документа, программная запись отклоняется"},
		}),
		"enum": {
			"$schema":              "https://json-schema.org/draft/2020-12/schema",
			"title":                "OneBase enum",
			"type":                 "object",
			"required":             []string{"name", "values"},
			"additionalProperties": false,
			"properties": map[string]any{
				"name": stringSchema("Имя перечисления"),
				"values": map[string]any{"type": "array", "items": map[string]any{"oneOf": []any{
					stringSchema("Имя значения"),
					map[string]any{"type": "object", "properties": map[string]any{"name": stringSchema("Имя"), "titles": stringMapSchema()}, "required": []string{"name"}},
				}}},
			},
		},
		"constants": {
			"$schema":              "https://json-schema.org/draft/2020-12/schema",
			"title":                "OneBase constants file",
			"type":                 "object",
			"required":             []string{"constants"},
			"additionalProperties": false,
			"properties": map[string]any{"constants": arrayOf(map[string]any{
				"type": "object", "required": []string{"name", "type"},
				"properties": map[string]any{"name": stringSchema("Имя"), "type": stringSchema("Тип"), "default": stringSchema("Значение"), "label": stringSchema("Подпись"), "labels": stringMapSchema()},
			})},
		},
		"processor": {
			"$schema":              "https://json-schema.org/draft/2020-12/schema",
			"title":                "OneBase processor",
			"type":                 "object",
			"required":             []string{"name"},
			"additionalProperties": true,
			"properties":           map[string]any{"name": stringSchema("Имя"), "title": stringSchema("Синоним"), "params": arrayOf(param), "tableparts": arrayOf(tablePart)},
		},
		"report": {
			"$schema":              "https://json-schema.org/draft/2020-12/schema",
			"title":                "OneBase report",
			"type":                 "object",
			"required":             []string{"name"},
			"additionalProperties": true,
			"properties":           map[string]any{"name": stringSchema("Имя"), "title": stringSchema("Синоним"), "params": arrayOf(param), "query": stringSchema("Текст запроса"), "chart_proc": stringSchema("Процедура диаграммы"), "composition": map[string]any{"type": "object"}},
		},
		"widget": {
			"$schema":              "https://json-schema.org/draft/2020-12/schema",
			"title":                "OneBase widget",
			"type":                 "object",
			"required":             []string{"name", "type"},
			"additionalProperties": false,
			"properties": map[string]any{
				"name": stringSchema("Имя"), "type": enumSchema("kpi", "list", "chart", "actions", "recent"), "title": stringSchema("Заголовок"), "titles": stringMapSchema(),
				"query": stringSchema("Запрос"), "params": stringMapSchema(), "format": stringSchema("money|number|percent"), "compare_to": stringSchema("prev_period"), "limit": map[string]any{"type": "integer"},
				"columns":    arrayOf(map[string]any{"type": "object", "properties": map[string]any{"field": stringSchema("Поле"), "label": stringSchema("Подпись"), "labels": stringMapSchema(), "format": stringSchema("Формат"), "align": stringSchema("left|right|center")}}),
				"chart_kind": stringSchema("bar|line|pie"), "chart_type": stringSchema("legacy alias: bar|line|pie"), "x_field": stringSchema("Поле X"), "y_fields": arrayOf(stringSchema("Поле Y")),
				"items":    arrayOf(map[string]any{"type": "object", "properties": map[string]any{"label": stringSchema("Подпись"), "labels": stringMapSchema(), "entity": stringSchema("Объект"), "url": stringSchema("URL")}}),
				"entities": arrayOf(stringSchema("Объект")), "scope": stringSchema("current_user|all"),
			},
		},
		"form":      looseNamedSchema("OneBase managed form"),
		"role":      looseNamedSchema("OneBase RBAC role"),
		"page":      looseNamedSchema("OneBase page"),
		"service":   looseNamedSchema("OneBase HTTP service"),
		"subsystem": looseNamedSchema("OneBase subsystem"),
		"journal":   looseNamedSchema("OneBase document journal"),
		"scheduled": looseNamedSchema("OneBase scheduled job"),
		"accounts":  looseNamedSchema("OneBase chart of accounts"),
		"accountreg": fieldGroupSchema("OneBase accounting register", field, []string{"resources", "subconto"}, map[string]any{
			"accounts": stringSchema("Имя плана счетов"),
		}),
		"home-page": looseNamedSchema("OneBase home page"),
	}
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func stringMapSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}
}

func enumSchema(vals ...string) map[string]any {
	return map[string]any{"type": "string", "enum": vals}
}

func arrayOf(item any) map[string]any {
	return map[string]any{"type": "array", "items": item}
}

func fieldGroupSchema(title string, field map[string]any, groups []string, extra map[string]any) map[string]any {
	props := map[string]any{"name": stringSchema("Имя"), "title": stringSchema("Синоним"), "titles": stringMapSchema()}
	for _, g := range groups {
		props[g] = arrayOf(field)
	}
	for k, v := range extra {
		props[k] = v
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                title,
		"type":                 "object",
		"required":             []string{"name"},
		"additionalProperties": false,
		"properties":           props,
	}
}

func looseNamedSchema(title string) map[string]any {
	return map[string]any{
		"$schema":  "https://json-schema.org/draft/2020-12/schema",
		"title":    title,
		"type":     "object",
		"required": []string{"name"},
		"properties": map[string]any{
			"name":   stringSchema("Имя"),
			"title":  stringSchema("Синоним"),
			"titles": stringMapSchema(),
		},
		"additionalProperties": true,
	}
}
