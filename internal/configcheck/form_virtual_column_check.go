package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormVirtualColumns проверяет объявления виртуальных колонок табличной
// части (#845).
//
// Это ошибки, а не предупреждения. Виртуальная колонка живёт только на форме:
// неверный путь не ломает ни запись, ни схему — колонка просто не появится или
// окажется пустой. Молчащая форма хуже красной проверки: искать, почему колонка
// пуста, пришлось бы в браузере (тот же вывод, что и в #830).
func CheckFormVirtualColumns(proj *project.Project) []Issue {
	var issues []Issue
	byName := make(map[string]*metadata.Entity, len(proj.Entities))
	for _, ent := range proj.Entities {
		byName[strings.ToLower(ent.Name)] = ent
	}

	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			label := formFileLabel(ent, form)
			walkFormElements(form.Elements, func(el *metadata.FormElement) {
				if len(el.VirtualColumns) == 0 {
					return
				}
				add := func(msg, fix string) {
					issues = append(issues, Issue{
						File:         label,
						Object:       ent.Name,
						Kind:         "Управляемая форма",
						Code:         "form.virtual-column",
						Message:      fmt.Sprintf("элемент %q: %s", formElementName(el), msg),
						SuggestedFix: fix,
					})
				}
				tp := virtualColumnTablePart(ent, el)
				if tp == nil {
					add("virtual_columns объявлены не на табличной части",
						"перенесите ключ на элемент kind: ТабличнаяЧасть этой сущности")
					return
				}
				seen := make(map[string]bool, len(el.VirtualColumns))
				for _, vc := range el.VirtualColumns {
					checkVirtualColumn(add, byName, *tp, vc, seen)
				}
			})
		}
	}
	return issues
}

func checkVirtualColumn(
	add func(msg, fix string),
	entities map[string]*metadata.Entity,
	tp metadata.TablePart,
	vc metadata.FormVirtualColumn,
	seen map[string]bool,
) {
	name := strings.TrimSpace(vc.Name)
	if name == "" {
		add("виртуальная колонка без name", "задайте name — по нему колонка адресуется внутри формы")
		return
	}
	// Имя колонки не должно совпадать с реквизитом ТЧ: одноимённая виртуальная
	// колонка перекрыла бы хранимое значение в той же строке — на форме одно, в
	// базе другое.
	for _, f := range tp.Fields {
		if strings.EqualFold(f.Name, name) {
			add(fmt.Sprintf("виртуальная колонка %q совпадает с реквизитом табличной части", name),
				"дайте колонке другое имя: одноимённая перекрыла бы хранимое значение")
			return
		}
	}
	if seen[strings.ToLower(name)] {
		add(fmt.Sprintf("виртуальная колонка %q объявлена дважды", name), "оставьте одно объявление")
		return
	}
	seen[strings.ToLower(name)] = true

	refName, ok := vc.RefFieldName()
	if !ok {
		add(fmt.Sprintf("колонка %q: data_path %q не является путём из двух сегментов", name, vc.DataPath),
			"формат пути — «<ссылочный реквизит строки>.<реквизит целевого объекта>», например Клиент.Код")
		return
	}
	targetName, _ := vc.TargetFieldName()

	var refField *metadata.Field
	for i := range tp.Fields {
		if strings.EqualFold(tp.Fields[i].Name, refName) {
			refField = &tp.Fields[i]
			break
		}
	}
	if refField == nil {
		add(fmt.Sprintf("колонка %q: в табличной части нет реквизита %q", name, refName),
			"первый сегмент пути — реквизит ЭТОЙ табличной части")
		return
	}
	if refField.RefEntity == "" {
		add(fmt.Sprintf("колонка %q: реквизит %q не ссылочный (%s)", name, refName, refField.Type),
			"разворачивать по точке можно только ссылку на справочник или документ")
		return
	}
	target := entities[strings.ToLower(refField.RefEntity)]
	if target == nil {
		// Сама по себе неизвестная сущность в ссылке — забота CheckCrossRefs;
		// здесь просто нечего проверять дальше.
		return
	}
	for _, f := range target.Fields {
		if strings.EqualFold(f.Name, targetName) {
			return
		}
	}
	// Номер документа в fields: не объявлен — его синтезирует блок numerator,
	// но в строке он есть и показать его законно.
	if target.Kind == metadata.KindDocument && strings.EqualFold(targetName, "Номер") {
		return
	}
	add(fmt.Sprintf("колонка %q: у %s нет реквизита %q", name, refField.RefEntity, targetName),
		"второй сегмент пути — реквизит шапки целевого объекта")
}

// virtualColumnTablePart находит табличную часть, к которой относится элемент:
// ключ table_part или data_path «Объект.<ТЧ>». Возвращает nil, если элемент —
// не табличная часть сущности (например таблица значений формы).
func virtualColumnTablePart(ent *metadata.Entity, el *metadata.FormElement) *metadata.TablePart {
	name := strings.TrimSpace(el.TablePart)
	if name == "" {
		if parts := strings.Split(el.DataPath, "."); len(parts) == 2 &&
			strings.EqualFold(strings.TrimSpace(parts[0]), "Объект") {
			name = strings.TrimSpace(parts[1])
		}
	}
	if name == "" {
		return nil
	}
	for i := range ent.TableParts {
		if strings.EqualFold(ent.TableParts[i].Name, name) {
			return &ent.TableParts[i]
		}
	}
	return nil
}
