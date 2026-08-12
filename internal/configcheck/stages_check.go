package configcheck

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckStages возвращает НЕблокирующие предупреждения по блоку `stages`
// (план 121).
//
// Ошибки объявления — неизвестный реквизит, не-перечисление, недостижимый этап,
// переход из необъявленного состояния — ловит metadata.Validate, и до сюда
// конфигурация с ними не доезжает. Здесь остаётся то, что формально верно, но
// на практике означает не то, что автор думал:
//
//   - значения перечисления, не вошедшие в `order` — объект может оказаться вне
//     маршрута, и ни гейт, ни отчёт «где застряло» про такое состояние не знают;
//   - `enforce: warn` — маршрут объявлен, но не соблюдается: нарушение только
//     пишется в лог. Это умолчание, и оно осознанное (включение блока в
//     работающей конфигурации не должно ломать накопленные данные), но
//     «гарантии переходов» у такой конфигурации нет.
//
// Проверить нарушения в САМИХ данных здесь нельзя: `onebase check` работает с
// временной базой, где есть схема и нет ни одной строки (BuildSchemaDB). Такой
// отчёт даёт интерфейс — раздел «где застряло» показывает строку «вне
// маршрута» по реальной базе.
func CheckStages(proj *project.Project) []Issue {
	var warns []Issue
	for _, e := range proj.Entities {
		if e == nil || e.Stages == nil {
			continue
		}
		s := e.Stages
		file := entityFileLabel(e)

		if enum := findEnum(proj, e); enum != nil {
			var missing []string
			for _, v := range enum.Values {
				if !s.Known(v) {
					missing = append(missing, v)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				warns = append(warns, Issue{
					File:   file,
					Object: e.Name,
					Kind:   string(e.Kind),
					Code:   "stages.value-outside-route",
					Message: fmt.Sprintf("этапы %s: значения перечисления %s не вошли в order — %s. Объект в таком состоянии выпадает из маршрута: переход в него и из него не описан, в отчёте «где застряло» он не считается",
						e.Name, s.Field, strings.Join(missing, ", ")),
					SuggestedFix: "добавьте значения в stages.order или уберите их из перечисления",
				})
			}
		}

		if !s.Strict() {
			warns = append(warns, Issue{
				File:   file,
				Object: e.Name,
				Kind:   string(e.Kind),
				Code:   "stages.enforce-warn",
				Message: fmt.Sprintf("этапы %s: enforce: warn — недопустимый переход только пишется в лог, запись проходит. Гарантии маршрута нет",
					e.Name),
				SuggestedFix: "убедитесь, что накопленные данные маршруту соответствуют, и включите enforce: strict",
			})
		}
	}
	return warns
}

// findEnum возвращает перечисление, на котором сущность ведёт этапы.
func findEnum(proj *project.Project, e *metadata.Entity) *metadata.Enum {
	f := e.StageField()
	if f == nil || f.EnumName == "" {
		return nil
	}
	for _, en := range proj.Enums {
		if en != nil && strings.EqualFold(en.Name, f.EnumName) {
			return en
		}
	}
	return nil
}

// entityFileLabel — путь к файлу сущности в том же виде, в каком его печатают
// остальные проверки: catalogs/<имя>.yaml или documents/<имя>.yaml.
func entityFileLabel(e *metadata.Entity) string {
	dir := "catalogs"
	if e.Kind == metadata.KindDocument {
		dir = "documents"
	}
	return dir + "/" + strings.ToLower(e.Name) + ".yaml"
}
