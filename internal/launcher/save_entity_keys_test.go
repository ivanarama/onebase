package launcher

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Конфигуратор сохраняет объект round-trip'ом через saveEntity: Unmarshal →
// правка реквизитов → Marshal. Любой верхнеуровневый ключ YAML, которого нет в
// saveEntity, при этом молча теряется.
//
// Так уже дважды и происходило. В 2026-05-25 — с hierarchical, numerator и
// predefined: после добавления реквизита у справочника пропадали группы и
// деревья. Список ключей тогда пополнили руками, и он снова отстал: к
// 2026-08-10 терялись tile_view, indexes, list_mode, list_refresh_on,
// notify_changes и description (issue #670, этап 118A).
//
// Пополнять список руками — значит ждать третьего раза. Этот тест сверяет
// saveEntity с rawEntity из internal/metadata по yaml-тегам: добавили ключ в
// метаданные — либо добавьте его в saveEntity, либо внесите в
// saveEntityExempt с объяснением, почему конфигуратор вправе его терять.

// saveEntityExempt — ключи rawEntity, которых в saveEntity намеренно нет.
// Ключ карты — имя yaml-тега, значение — обоснование.
var saveEntityExempt = map[string]string{}

// yamlTagsOfStruct разбирает исходник Go и возвращает имена yaml-тегов полей
// указанной структуры. Читаем исходник, а не рефлексию: rawEntity не
// экспортирована, из другого пакета до неё не дотянуться.
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

func TestSaveEntity_CoversAllRawKeys(t *testing.T) {
	raw := yamlTagsOfStruct(t, "../metadata/yaml.go", "rawEntity")
	save := yamlTagsOfStruct(t, "configurator_types.go", "saveEntity")

	have := make(map[string]bool, len(save))
	for _, k := range save {
		have[k] = true
	}
	var missing []string
	for _, k := range raw {
		if have[k] {
			continue
		}
		if _, ok := saveEntityExempt[k]; ok {
			continue
		}
		missing = append(missing, k)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("saveEntity не знает ключи rawEntity (%d): %s\n\n"+
			"При сохранении объекта из конфигуратора они молча пропадут из YAML.\n"+
			"Добавьте их в saveEntity (нередактируемые — сырым *yaml.Node) либо\n"+
			"внесите в saveEntityExempt с объяснением, почему их можно терять.",
			len(missing), strings.Join(missing, ", "))
	}
}

// Список исключений не должен превращаться в свалку: запись про ключ, которого
// в rawEntity уже нет, вводит в заблуждение.
func TestSaveEntityExempt_IsAlive(t *testing.T) {
	raw := yamlTagsOfStruct(t, "../metadata/yaml.go", "rawEntity")
	known := make(map[string]bool, len(raw))
	for _, k := range raw {
		known[k] = true
	}
	var stale []string
	for k, reason := range saveEntityExempt {
		if !known[k] {
			stale = append(stale, k+" — такого ключа в rawEntity нет")
		}
		if strings.TrimSpace(reason) == "" {
			stale = append(stale, k+" — исключение без обоснования")
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("протухшие записи в saveEntityExempt (%d):\n  %s", len(stale), strings.Join(stale, "\n  "))
	}
}
