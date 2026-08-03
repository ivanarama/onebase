package launcher

import (
	"encoding/json"
	"html/template"
	"strings"
)

// layout_meta.go — метаданные для панели «Данные» визуального дизайнера макетов
// (план 64, этап 5b, пункт 6.5). buildLayoutMeta собирает JSON, который JS-панель
// использует, чтобы показать дерево «Реквизиты документа / Табличные части /
// Константы» и привязать поле к параметру ячейки.

// ldMetaField — поле сущности (реквизит или колонка ТЧ) для дерева данных.
type ldMetaField struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Ref  string `json:"ref,omitempty"` // имя ссылочной сущности (для .Наименование)
}

// ldMetaTablePart — табличная часть с её колонками.
type ldMetaTablePart struct {
	Name   string        `json:"name"`
	Fields []ldMetaField `json:"fields"`
}

// ldMetaEntity — метаданные сущности (реквизиты + ТЧ) для панели данных.
type ldMetaEntity struct {
	Name       string            `json:"name"`
	Fields     []ldMetaField     `json:"fields"`
	TableParts []ldMetaTablePart `json:"tableParts"`
}

// ldMeta — корневой объект, сериализуемый в страницу (_ldMeta в JS).
//   - Entities: имя сущности (как есть) → метаданные;
//   - Constants: имена констант (источник для дерева «Константы»);
//   - FormDoc: имя макета (нижний регистр) → имя документа/сущности, к которой
//     он привязан (cfgDSLPrintForm.Document) — чтобы панель знала, чьи поля
//     показывать.
type ldMeta struct {
	Entities  map[string]ldMetaEntity `json:"entities"`
	Constants []string                `json:"constants"`
	FormDoc   map[string]string       `json:"formDoc"`
}

// buildLayoutMeta строит JSON метаданных для панели данных дизайнера макетов.
func buildLayoutMeta(d *configuratorData) template.JS {
	meta := ldMeta{
		Entities: make(map[string]ldMetaEntity, len(d.Entities)),
		FormDoc:  make(map[string]string, len(d.DSLPrintForms)),
	}

	for _, e := range d.Entities {
		me := ldMetaEntity{Name: e.Name}
		for _, f := range e.Fields {
			me.Fields = append(me.Fields, ldMetaField{Name: f.Name, Type: f.Type, Ref: f.RefEntity})
		}
		for _, tp := range e.TableParts {
			mtp := ldMetaTablePart{Name: tp.Name}
			for _, f := range tp.Fields {
				mtp.Fields = append(mtp.Fields, ldMetaField{Name: f.Name, Type: f.Type, Ref: f.RefEntity})
			}
			me.TableParts = append(me.TableParts, mtp)
		}
		meta.Entities[e.Name] = me
	}

	for _, c := range d.Constants {
		meta.Constants = append(meta.Constants, c.Name)
	}

	// Карта макет → документ: и для DSL-форм (.os + макет), и для standalone
	// деклараций (если попадут в DSLPrintForms). Имя макета в нижнем регистре.
	for _, pf := range d.DSLPrintForms {
		if pf.Document != "" {
			meta.FormDoc[strings.ToLower(pf.Name)] = pf.Document
		}
	}

	b, err := json.Marshal(meta)
	if err != nil {
		return template.JS("{}")
	}
	return template.JS(b) //nolint:gosec // G203: значение получено json.Marshal — он экранирует < > & в \u-последовательности, поэтому «</script>» из данных не разорвёт тег
}
