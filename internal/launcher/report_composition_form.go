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
// порядок и форматирование прочих ключей. val==nil удаляет ключ. Позволяет
// точечно править одно поле документа без round-trip через типизированную
// структуру (которая молча теряет неперечисленные в ней поля).
func setYAMLMapField(m *yaml.Node, key string, val any) error {
	mapping, err := resolveYAMLAlias(m)
	if err != nil {
		return err
	}
	existing, index, err := yamlMapField(mapping, key)
	if err != nil {
		return err
	}
	if existing != nil {
		if val == nil {
			// Удаление определения anchor при живых aliases создало бы битый YAML.
			// Alias-значение удалить безопасно: само определение находится в другом
			// узле и остаётся на месте.
			if existing.Kind != yaml.AliasNode && existing.Anchor != "" {
				return fmt.Errorf("setYAMLMapField: нельзя удалить ключ %q с YAML-anchor %q", key, existing.Anchor)
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
		replaceYAMLNode(target, &vn)
		return nil
	}
	if val == nil {
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
