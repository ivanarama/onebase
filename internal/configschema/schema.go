package configschema

import (
	"fmt"
	"sort"
	"strings"
)

type Schema map[string]any

func Kinds() []string {
	out := make([]string, 0, len(schemas))
	for k := range schemas {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func Get(kind string) (Schema, bool) {
	key := normalizeKind(kind)
	build, ok := schemas[key]
	if !ok {
		return nil, false
	}
	return build(key), true
}

func MustGet(kind string) (Schema, error) {
	s, ok := Get(kind)
	if !ok {
		return nil, fmt.Errorf("неизвестный вид схемы %q", kind)
	}
	return s, nil
}

func normalizeKind(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	k = strings.ReplaceAll(k, "_", "-")
	if v, ok := aliases[k]; ok {
		return v
	}
	return k
}

var aliases = map[string]string{
	"catalog": "catalog", "справочник": "catalog",
	"document": "document", "документ": "document",
	"register": "register", "регистр": "register", "регистр-накопления": "register",
	"inforeg": "inforeg", "info-register": "inforeg", "регистр-сведений": "inforeg",
	"enum": "enum", "перечисление": "enum",
	"constants": "constants", "constant": "constants", "константы": "constants",
	"report": "report", "отчёт": "report", "отчет": "report",
	"processor": "processor", "обработка": "processor",
	"widget": "widget", "виджет": "widget",
	"form": "form", "форма": "form",
	"page": "page", "страница": "page",
	"service": "service", "http-service": "service", "сервис": "service",
	"role": "role", "роль": "role",
	"subsystem": "subsystem", "подсистема": "subsystem",
	"journal": "journal", "журнал": "journal",
	"scheduled": "scheduled", "job": "scheduled", "регламентное-задание": "scheduled",
	"account": "account", "accounts": "account", "план-счетов": "account",
	"accountreg": "accountreg", "account-register": "accountreg", "регистр-бухгалтерии": "accountreg",
	"home-page": "home-page", "homepage": "home-page",
	"app": "app", "app-config": "app",
}

var schemas = map[string]func(string) Schema{
	"catalog":    entitySchema("catalog", false),
	"document":   entitySchema("document", true),
	"register":   registerSchema,
	"inforeg":    infoRegisterSchema,
	"enum":       enumSchema,
	"constants":  constantsSchema,
	"report":     reportSchema,
	"processor":  processorSchema,
	"widget":     widgetSchema,
	"form":       formSchema,
	"page":       pageSchema,
	"service":    serviceSchema,
	"role":       roleSchema,
	"subsystem":  subsystemSchema,
	"journal":    journalSchema,
	"scheduled":  scheduledSchema,
	"account":    accountSchema,
	"accountreg": accountRegSchema,
	"home-page":  homePageSchema,
	"app":        appSchema,
}

func base(kind, title string, props map[string]any, required []string) Schema {
	return Schema{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://onebase.local/schema/" + kind + ".schema.json",
		"title":                title,
		"type":                 "object",
		"additionalProperties": true,
		"properties":           props,
		"required":             required,
	}
}

func entitySchema(kind string, document bool) func(string) Schema {
	return func(_ string) Schema {
		props := commonNamedProps()
		props["fields"] = arrayOf(fieldSchema())
		props["tableparts"] = arrayOf(object(map[string]any{
			"name":   str(),
			"title":  str(),
			"titles": stringMap(),
			"fields": arrayOf(fieldSchema()),
		}, []string{"name"}))
		props["list_form"] = arrayOf(str())
		props["item_form"] = arrayOf(str())
		props["based_on"] = arrayOf(str())
		props["hierarchical"] = boolSchema()
		props["hierarchy_kind"] = enum("folders", "items", "folders_and_items")
		props["list_mode"] = enum("paged", "feed")
		props["tile_view"] = object(map[string]any{
			"image":    str(),
			"title":    str(),
			"subtitle": str(),
			"fields":   arrayOf(str()),
		}, nil)
		props["predefined"] = arrayOf(object(map[string]any{
			"name":   str(),
			"fields": mapSchema(anySchema()),
		}, []string{"name"}))
		if document {
			props["posting"] = boolSchema()
			props["numerator"] = object(map[string]any{
				"prefix": str(),
				"length": integer(),
				"period": enum("none", "day", "month", "quarter", "year"),
				"scope":  str(),
			}, nil)
		}
		title := "OneBase catalog metadata"
		if document {
			title = "OneBase document metadata"
		}
		return base(kind, title, props, []string{"name"})
	}
}

func registerSchema(kind string) Schema {
	props := commonNamedProps()
	props["kind"] = enum("balance", "turnover", "обороты")
	props["dimensions"] = arrayOf(fieldSchema())
	props["resources"] = arrayOf(fieldSchema())
	props["attributes"] = arrayOf(fieldSchema())
	return base(kind, "OneBase accumulation register metadata", props, []string{"name"})
}

func infoRegisterSchema(kind string) Schema {
	props := commonNamedProps()
	props["periodic"] = boolSchema()
	props["dimensions"] = arrayOf(fieldSchema())
	props["resources"] = arrayOf(fieldSchema())
	return base(kind, "OneBase information register metadata", props, []string{"name"})
}

func enumSchema(kind string) Schema {
	return base(kind, "OneBase enum metadata", map[string]any{
		"name": str(),
		"values": arrayOf(anyOf(str(), object(map[string]any{
			"name":   str(),
			"titles": stringMap(),
		}, []string{"name"}))),
	}, []string{"name", "values"})
}

func constantsSchema(kind string) Schema {
	return base(kind, "OneBase constants metadata", map[string]any{
		"constants": arrayOf(object(map[string]any{
			"name":    str(),
			"type":    fieldType(),
			"default": anySchema(),
			"label":   str(),
			"labels":  stringMap(),
		}, []string{"name", "type"})),
	}, []string{"constants"})
}

func reportSchema(kind string) Schema {
	props := commonNamedProps()
	props["params"] = arrayOf(paramSchema())
	props["query"] = str()
	props["chart_proc"] = str()
	props["composition"] = compositionSchema()
	props["variants"] = arrayOf(object(map[string]any{
		"name":        str(),
		"composition": compositionSchema(),
	}, []string{"name"}))
	return base(kind, "OneBase report metadata", props, []string{"name"})
}

func processorSchema(kind string) Schema {
	props := commonNamedProps()
	props["params"] = arrayOf(paramSchema())
	props["table_parts"] = arrayOf(object(map[string]any{
		"name":   str(),
		"fields": arrayOf(fieldSchema()),
	}, []string{"name"}))
	return base(kind, "OneBase processor metadata", props, []string{"name"})
}

func widgetSchema(kind string) Schema {
	props := commonNamedProps()
	props["type"] = enum("kpi", "list", "chart", "actions", "recent")
	props["query"] = str()
	props["params"] = stringMap()
	props["format"] = enum("money", "number", "percent", "date")
	props["compare_to"] = enum("prev_period")
	props["limit"] = integer()
	props["columns"] = arrayOf(object(map[string]any{
		"field":  str(),
		"label":  str(),
		"labels": stringMap(),
		"format": str(),
		"align":  enum("left", "right", "center"),
	}, []string{"field"}))
	props["chart_kind"] = enum("bar", "line", "pie")
	props["x_field"] = str()
	props["y_fields"] = arrayOf(str())
	props["items"] = arrayOf(object(map[string]any{
		"label":  str(),
		"labels": stringMap(),
		"entity": str(),
		"url":    str(),
	}, nil))
	props["entities"] = arrayOf(str())
	props["scope"] = enum("current_user", "all")
	return base(kind, "OneBase dashboard widget metadata", props, []string{"name", "type"})
}

func formSchema(kind string) Schema {
	return base(kind, "OneBase managed form metadata", map[string]any{
		"schema": enum("onebase.form/v1"),
		"form": object(map[string]any{
			"name":               str(),
			"kind":               enum("object", "list", "choice", "folder", "custom"),
			"entity":             str(),
			"title":              stringMap(),
			"original_id":        str(),
			"auto_save_settings": boolSchema(),
			"vertical_scroll":    str(),
		}, []string{"name"}),
		"attributes":  arrayOf(anySchema()),
		"commands":    arrayOf(anySchema()),
		"command_bar": anySchema(),
		"elements":    arrayOf(formElementSchema()),
		"events":      stringMap(),
		"actions":     mapSchema(anySchema()),
		"oneC_meta":   mapSchema(anySchema()),
	}, []string{"form"})
}

func pageSchema(kind string) Schema {
	props := commonNamedProps()
	props["icon"] = str()
	props["roles"] = arrayOf(str())
	props["params"] = arrayOf(str())
	return base(kind, "OneBase page metadata", props, []string{"name"})
}

func serviceSchema(kind string) Schema {
	props := commonNamedProps()
	props["root_url"] = str()
	props["auth"] = enum("none", "basic", "session", "token", "hmac")
	props["secret"] = str()
	props["rate_limit"] = integer()
	props["roles"] = arrayOf(str())
	props["cors"] = object(map[string]any{
		"origins":     arrayOf(str()),
		"headers":     arrayOf(str()),
		"credentials": boolSchema(),
		"max_age":     integer(),
	}, nil)
	props["templates"] = arrayOf(object(map[string]any{
		"template": str(),
		"methods":  stringMap(),
	}, []string{"template"}))
	return base(kind, "OneBase HTTP service metadata", props, []string{"name"})
}

func roleSchema(kind string) Schema {
	permMap := mapSchema(arrayOf(str()))
	return base(kind, "OneBase RBAC role metadata", map[string]any{
		"name":        str(),
		"description": str(),
		"permissions": object(map[string]any{
			"catalogs":   permMap,
			"documents":  permMap,
			"registers":  permMap,
			"inforegs":   permMap,
			"reports":    permMap,
			"processors": permMap,
		}, nil),
	}, []string{"name"})
}

func subsystemSchema(kind string) Schema {
	props := commonNamedProps()
	props["icon"] = str()
	props["order"] = integer()
	props["contents"] = object(map[string]any{
		"catalogs":    arrayOf(str()),
		"documents":   arrayOf(str()),
		"reports":     arrayOf(str()),
		"processors":  arrayOf(str()),
		"pages":       arrayOf(str()),
		"widgets":     arrayOf(str()),
		"subsystems":  arrayOf(str()),
		"services":    arrayOf(str()),
		"journals":    arrayOf(str()),
		"registers":   arrayOf(str()),
		"inforegs":    arrayOf(str()),
		"accountregs": arrayOf(str()),
	}, nil)
	props["home_page"] = anySchema()
	return base(kind, "OneBase subsystem metadata", props, []string{"name"})
}

func journalSchema(kind string) Schema {
	props := commonNamedProps()
	props["documents"] = arrayOf(str())
	props["columns"] = arrayOf(anySchema())
	props["filters"] = arrayOf(anySchema())
	return base(kind, "OneBase document journal metadata", props, []string{"name"})
}

func scheduledSchema(kind string) Schema {
	props := commonNamedProps()
	props["schedule"] = str()
	props["processor"] = str()
	props["params"] = mapSchema(anySchema())
	props["enabled"] = boolSchema()
	props["on_error"] = enum("continue", "stop", "retry")
	props["timeout"] = integer()
	return base(kind, "OneBase scheduled job metadata", props, []string{"name", "schedule", "processor"})
}

func accountSchema(kind string) Schema {
	props := commonNamedProps()
	props["accounts"] = arrayOf(object(map[string]any{
		"code":   str(),
		"name":   str(),
		"kind":   enum("active", "passive", "active_passive"),
		"parent": str(),
	}, []string{"code", "name"}))
	return base(kind, "OneBase chart of accounts metadata", props, []string{"name"})
}

func accountRegSchema(kind string) Schema {
	props := commonNamedProps()
	props["accounts"] = str()
	props["resources"] = arrayOf(fieldSchema())
	props["subconto"] = arrayOf(fieldSchema())
	return base(kind, "OneBase accounting register metadata", props, []string{"name"})
}

func homePageSchema(kind string) Schema {
	return base(kind, "OneBase home page metadata", map[string]any{
		"sections": arrayOf(anySchema()),
		"widgets":  arrayOf(str()),
	}, nil)
}

func appSchema(kind string) Schema {
	return base(kind, "OneBase app config metadata", map[string]any{
		"name":        str(),
		"version":     str(),
		"author":      str(),
		"copyright":   str(),
		"license":     str(),
		"lang":        str(),
		"logo":        str(),
		"email":       mapSchema(anySchema()),
		"attachments": mapSchema(anySchema()),
		"demo":        mapSchema(anySchema()),
		"backup":      mapSchema(anySchema()),
		"llm":         mapSchema(anySchema()),
		"webhooks":    arrayOf(mapSchema(anySchema())),
	}, nil)
}

func commonNamedProps() map[string]any {
	return map[string]any{
		"name":   str(),
		"title":  str(),
		"titles": stringMap(),
	}
}

func fieldSchema() Schema {
	return object(map[string]any{
		"name":                str(),
		"title":               str(),
		"titles":              stringMap(),
		"type":                fieldType(),
		"allow_inline_create": boolSchema(),
	}, []string{"name", "type"})
}

func paramSchema() Schema {
	return object(map[string]any{
		"name":    str(),
		"type":    fieldType(),
		"label":   str(),
		"labels":  stringMap(),
		"default": anySchema(),
		"options": arrayOf(str()),
	}, []string{"name", "type"})
}

func compositionSchema() Schema {
	return object(map[string]any{
		"groupings": arrayOf(str()),
		"columns":   arrayOf(str()),
		"measures": arrayOf(object(map[string]any{
			"field":  str(),
			"agg":    enum("sum", "count", "avg", "min", "max"),
			"title":  str(),
			"align":  enum("left", "right", "center"),
			"format": str(),
			"expr":   str(),
		}, []string{"field"})),
		"totals": object(map[string]any{
			"grand":     boolSchema(),
			"subtotals": boolSchema(),
		}, nil),
		"detail": boolSchema(),
		"sort": arrayOf(object(map[string]any{
			"field": str(),
			"dir":   enum("asc", "desc"),
		}, []string{"field"})),
		"conditional":   arrayOf(anySchema()),
		"appearance":    mapSchema(anySchema()),
		"chart":         mapSchema(anySchema()),
		"detail_link":   str(),
		"detail_entity": str(),
	}, nil)
}

func formElementSchema() Schema {
	return object(map[string]any{
		"name":       str(),
		"kind":       str(),
		"title":      stringMap(),
		"field":      str(),
		"data_path":  str(),
		"table_part": str(),
		"required":   boolSchema(),
		"hint":       str(),
		"children":   arrayOf(anySchema()),
	}, nil)
}

func fieldType() Schema {
	return Schema{
		"type":        "string",
		"description": "string|text|number|date|bool|reference:<Object>|enum:<Enum>|number(L,P)",
	}
}

func object(props map[string]any, required []string) Schema {
	s := Schema{"type": "object", "additionalProperties": true, "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func mapSchema(value any) Schema {
	return Schema{"type": "object", "additionalProperties": value}
}

func stringMap() Schema {
	return mapSchema(str())
}

func arrayOf(item any) Schema {
	return Schema{"type": "array", "items": item}
}

func str() Schema {
	return Schema{"type": "string"}
}

func integer() Schema {
	return Schema{"type": "integer"}
}

func boolSchema() Schema {
	return Schema{"type": "boolean"}
}

func anySchema() Schema {
	return Schema{}
}

func enum(values ...string) Schema {
	return Schema{"type": "string", "enum": values}
}

func anyOf(items ...any) Schema {
	return Schema{"anyOf": items}
}
