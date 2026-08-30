package cli

// `onebase schema` — машинный контракт YAML: по нему редактор подчёркивает
// ошибки. Объект `field` объявлен с additionalProperties:false, поэтому ключ,
// который движок читает, а схема не описывает, превращается в ложную ошибку на
// корректной конфигурации.
//
// Так и накопилось: `properties` объекта `field` отстал от metadata.rawField
// сразу на четыре ключа — `id` (план 81), `required` (#1033), `default`
// (план 153) и `pii` — и никто этого не заметил, потому что сверять их было
// нечем. Сторож полноты был только у конфигуратора
// (TestSaveField_CoversAllRawKeys), у схемы — нет.
//
// Тест смотрит на ВЫВОД команды, а не на allSchemas(): редактор читает именно
// его, и проверка обязана идти той же дверью.

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// schemaFieldExempt — ключи rawField, которых в объекте `field` намеренно нет.
// Ключ карты — имя yaml-тега, значение — обоснование, почему ключ не публикуется.
//
// Пустая карта — не «список на будущее», а утверждение: сейчас публикуется всё,
// что движок читает. Запись сюда — осознанное решение не публиковать ключ, и
// TestSchemaFieldExempt_IsAlive следит, чтобы она не протухла.
var schemaFieldExempt = map[string]string{}

// yamlTagsOfStruct разбирает исходник Go и возвращает имена yaml-тегов полей
// указанной структуры. Читаем исходник, а не рефлексию: rawField не
// экспортирована, из пакета cli до неё не дотянуться. Своя копия помощника (в
// internal/launcher есть такая же) — по той же причине: он живёт в тестовом
// файле чужого пакета.
func yamlTagsOfStruct(t *testing.T, file, typeName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("разбор %s: %v", file, err)
	}
	var out []string
	ast.Inspect(af, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != typeName {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, f := range st.Fields.List {
			if f.Tag == nil {
				continue
			}
			tag := reflect.StructTag(strings.Trim(f.Tag.Value, "`")).Get("yaml")
			name := strings.TrimSpace(strings.Split(tag, ",")[0])
			if name != "" && name != "-" {
				out = append(out, name)
			}
		}
		return false
	})
	if len(out) == 0 {
		t.Fatalf("в %s не найдено полей структуры %s — сломан разбор", file, typeName)
	}
	sort.Strings(out)
	return out
}

// publishedSchema печатает контракт так же, как это делает пользователь:
// `onebase schema` без аргумента, и разбирает напечатанный JSON.
func publishedSchema(t *testing.T) map[string]any {
	t.Helper()
	out, err := captureStdout(t, func() error { return runSchema(schemaCmd, nil) })
	if err != nil {
		t.Fatalf("onebase schema: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("вывод команды — не JSON: %v\n%s", err, out)
	}
	return doc
}

// schemaAt спускается по опубликованному документу и возвращает объект по пути.
func schemaAt(t *testing.T, doc map[string]any, path ...string) map[string]any {
	t.Helper()
	cur := doc
	for i, key := range path {
		next, ok := cur[key].(map[string]any)
		if !ok {
			t.Fatalf("в опубликованной схеме нет пути %s (оборвался на %q): %#v",
				strings.Join(path, "."), path[i], cur[key])
		}
		cur = next
	}
	return cur
}

// fieldObjectPaths — места опубликованного документа, где пользователь пишет
// реквизит. Объект `field` в коде один, но проверять надо каждый выход: именно
// его читает редактор, открывший файл справочника, документа или регистра.
var fieldObjectPaths = map[string][]string{
	"реквизит шапки":        {"$defs", "entity", "properties", "fields", "items"},
	"поле табличной части":  {"$defs", "entity", "properties", "tableparts", "items", "properties", "fields", "items"},
	"измерение регистра":    {"$defs", "register", "properties", "dimensions", "items"},
	"ресурс регистра свед.": {"$defs", "inforeg", "properties", "resources", "items"},
	"ресурс бухрегистра":    {"$defs", "accountreg", "properties", "resources", "items"},
}

func TestSchemaField_CoversAllRawKeys(t *testing.T) {
	raw := yamlTagsOfStruct(t, "../metadata/yaml.go", "rawField")
	doc := publishedSchema(t)

	for name, path := range fieldObjectPaths {
		field := schemaAt(t, doc, path...)
		if field["additionalProperties"] != false {
			// Если объект перестал быть закрытым, тест обязан это заметить:
			// иначе он молча превращается в проверку ничего.
			t.Fatalf("%s: additionalProperties = %#v, ожидалось false", name, field["additionalProperties"])
		}
		props, ok := field["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s: properties = %#v", name, field["properties"])
		}
		var missing []string
		for _, key := range raw {
			if _, ok := props[key]; ok {
				continue
			}
			if _, exempt := schemaFieldExempt[key]; exempt {
				continue
			}
			missing = append(missing, key)
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s: схема не описывает ключи rawField (%d): %s\n\n"+
				"При additionalProperties:false редактор подчеркнёт их как ошибку в\n"+
				"корректном YAML. Добавьте ключ в объект field (internal/cli/schema.go)\n"+
				"либо впишите в schemaFieldExempt с обоснованием.",
				name, len(missing), strings.Join(missing, ", "))
		}
	}
}

// Список исключений не должен превращаться в свалку: запись про ключ, которого
// в rawField уже нет или который в схеме уже описан, вводит в заблуждение
// сильнее, чем её отсутствие.
func TestSchemaFieldExempt_IsAlive(t *testing.T) {
	raw := yamlTagsOfStruct(t, "../metadata/yaml.go", "rawField")
	known := make(map[string]bool, len(raw))
	for _, k := range raw {
		known[k] = true
	}
	props, _ := schemaAt(t, publishedSchema(t),
		"$defs", "entity", "properties", "fields", "items")["properties"].(map[string]any)

	var stale []string
	for k, reason := range schemaFieldExempt {
		if !known[k] {
			stale = append(stale, k+" — такого ключа в rawField нет")
		}
		if _, ok := props[k]; ok {
			stale = append(stale, k+" — ключ уже описан в схеме, исключение лишнее")
		}
		if strings.TrimSpace(reason) == "" {
			stale = append(stale, k+" — исключение без обоснования")
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("протухшие записи в schemaFieldExempt (%d):\n  %s", len(stale), strings.Join(stale, "\n  "))
	}
}

// Ключ `default` читается как СЫРОЙ СКАЛЯР (metadata.defaultScalar): `сейчас`,
// `12` и `Истина` YAML отдаёт строкой, числом и булевым. Схема, разрешающая
// только строку, подчёркивала бы `default: 12` на числовом реквизите — то есть
// самое естественное написание.
func TestSchemaFieldDefaultAcceptsEveryScalar(t *testing.T) {
	props := schemaAt(t, publishedSchema(t),
		"$defs", "entity", "properties", "fields", "items", "properties")
	def, ok := props["default"].(map[string]any)
	if !ok {
		t.Fatalf("default = %#v", props["default"])
	}
	types, ok := def["type"].([]any)
	if !ok {
		t.Fatalf("default.type = %#v, ожидался список допустимых скаляров", def["type"])
	}
	have := make(map[string]bool, len(types))
	for _, v := range types {
		s, _ := v.(string)
		have[s] = true
	}
	for _, want := range []string{"string", "number", "boolean"} {
		if !have[want] {
			t.Errorf("default не принимает %s: %#v", want, types)
		}
	}
}

// Типы новых ключей проверяются отдельно: описать ключ мало — `required: true`
// на схеме со "type": "string" подчёркивается ровно так же, как неописанный.
func TestSchemaFieldFlagsAreTyped(t *testing.T) {
	props := schemaAt(t, publishedSchema(t),
		"$defs", "entity", "properties", "fields", "items", "properties")
	for key, want := range map[string]string{"id": "string", "required": "boolean", "pii": "boolean"} {
		obj, ok := props[key].(map[string]any)
		if !ok {
			t.Errorf("%s = %#v", key, props[key])
			continue
		}
		if obj["type"] != want {
			t.Errorf("%s: type = %#v, ожидалось %q", key, obj["type"], want)
		}
		if desc, _ := obj["description"].(string); strings.TrimSpace(desc) == "" {
			t.Errorf("%s: ключ опубликован без описания — редактору нечего подсказать", key)
		}
	}
}
