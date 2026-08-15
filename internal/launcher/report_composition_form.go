package launcher

import (
	"fmt"
	"net/url"

	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compform"
	"gopkg.in/yaml.v3"
)

// parseCompositionForm — тонкая обёртка над compform.Parse. Сборщик вынесен в
// internal/report/compform, чтобы переиспользоваться рантаймом отчёта (план 70);
// обёртка сохранена ради существующих вызовов и тестов конфигуратора.
func parseCompositionForm(f url.Values) (*report.Composition, bool) {
	return compform.Parse(f)
}

// applyReportComposition обновляет блок composition в сыром YAML отчёта по форме.
// Если в форме нет comp.present — YAML возвращается без изменений. Иначе блок
// composition точечно перезаписывается (или удаляется, если форма пуста) прямо
// в дереве YAML, чтобы остальные поля отчёта — мультиязычные titles, params и
// любые будущие — сохранялись как есть. Раньше функция round-trip'ила YAML через
// частичную структуру без поля Titles и молча теряла titles (issue #86).
func applyReportComposition(raw []byte, f url.Values) ([]byte, error) {
	c, present := parseCompositionForm(f)
	if !present {
		return raw, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("applyReportComposition: ожидалось YAML-отображение в корне отчёта")
	}
	var val any
	if c != nil {
		val = c // c==nil (пустая форма) → ключ composition удаляется
	}
	if err := setYAMLMapField(root.Content[0], "composition", val); err != nil {
		return nil, err
	}
	return yaml.Marshal(&root)
}

// setYAMLMapField устанавливает значение ключа в mapping-узле YAML, сохраняя
// порядок и форматирование прочих ключей. val==nil убирает эффективное значение:
// удаляет обычный ключ, а при наличии значения из YAML merge оставляет явный
// null, чтобы старое значение не проявилось снова. Позволяет точечно править одно
// поле документа без round-trip через типизированную структуру (которая молча
// теряет неперечисленные в ней поля).
func setYAMLMapField(m *yaml.Node, key string, val any) error {
	mapping, err := resolveYAMLAlias(m)
	if err != nil {
		return err
	}
	existing, index, err := yamlMapField(mapping, key)
	if err != nil {
		return err
	}
	inherited, err := yamlMapHasMergedField(mapping, key)
	if err != nil {
		return err
	}
	if existing != nil {
		if val == nil {
			if inherited {
				// Deleting the direct value would reveal the value from << again.
				// Keep an explicit null override instead. If the direct value is an
				// alias, replace the alias edge itself rather than mutating its target.
				var vn yaml.Node
				if err := vn.Encode(nil); err != nil {
					return err
				}
				if existing.Kind != yaml.AliasNode && yamlNodeDescendantsDefineAnchor(existing) {
					return fmt.Errorf("setYAMLMapField: нельзя очистить ключ %q с вложенным YAML-anchor", key)
				}
				replaceYAMLNode(existing, &vn)
				return nil
			}
			// Удаление любого anchor в паре (включая anchor на ключе или во
			// вложенном значении) может оставить внешний alias без определения.
			// Alias-значение удалить безопасно: его определение находится в другом
			// узле и остаётся на месте.
			if yamlNodeDefinesAnchor(mapping.Content[index]) || yamlNodeDefinesAnchor(existing) {
				return fmt.Errorf("setYAMLMapField: нельзя удалить ключ %q с YAML-anchor", key)
			}
			mapping.Content = append(mapping.Content[:index], mapping.Content[index+2:]...)
			return nil
		}
		var vn yaml.Node
		if err := vn.Encode(val); err != nil {
			return err
		}
		target, err := resolveYAMLAlias(existing)
		if err != nil {
			return err
		}
		if yamlNodeDescendantsDefineAnchor(target) {
			return fmt.Errorf("setYAMLMapField: нельзя заменить ключ %q с вложенным YAML-anchor", key)
		}
		replaceYAMLNode(target, &vn)
		return nil
	}
	if val == nil {
		if !inherited {
			return nil
		}
		// There is no direct key, but << supplies one. An explicit null is the
		// only non-destructive way to shadow it without rewriting the merge graph.
		var vn yaml.Node
		if err := vn.Encode(nil); err != nil {
			return err
		}
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			&vn)
		return nil
	}
	var vn yaml.Node
	if err := vn.Encode(val); err != nil {
		return err
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&vn)
	return nil
}
