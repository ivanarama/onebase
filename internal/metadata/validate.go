package metadata

import (
	"fmt"
	"regexp"
	"strings"
)

// ValidateConstants проверяет, что enum-/reference-константы ссылаются на
// существующие перечисления и сущности — ловит опечатки в `type:` на
// `onebase check`, до рантайма (как Validate это делает для реквизитов
// сущностей). Раньше типы констант нигде не сверялись, а при опечатке
// GetEnum/GetEntity молча возвращали nil.
func ValidateConstants(constants []*Constant, entities []*Entity, enums []*Enum) error {
	entityNames := make(map[string]bool, len(entities))
	for _, e := range entities {
		entityNames[e.Name] = true
	}
	enumNames := make(map[string]bool, len(enums))
	for _, en := range enums {
		enumNames[en.Name] = true
	}
	for _, c := range constants {
		if c.RefEntity != "" && !entityNames[c.RefEntity] {
			return fmt.Errorf("constant %s references unknown entity %s", c.Name, c.RefEntity)
		}
		if c.EnumName != "" && !enumNames[c.EnumName] {
			return fmt.Errorf("constant %s references unknown enum %s", c.Name, c.EnumName)
		}
	}
	return nil
}

func Validate(entities []*Entity, enums []*Enum) error {
	entityNames := make(map[string]bool, len(entities))
	for _, e := range entities {
		entityNames[e.Name] = true
	}
	enumNames := make(map[string]bool, len(enums))
	for _, en := range enums {
		enumNames[en.Name] = true
	}
	for _, e := range entities {
		for _, f := range e.Fields {
			if f.RefEntity != "" && !entityNames[f.RefEntity] {
				return fmt.Errorf("entity %s: field %s references unknown entity %s", e.Name, f.Name, f.RefEntity)
			}
			if f.EnumName != "" && len(enums) > 0 && !enumNames[f.EnumName] {
				return fmt.Errorf("entity %s: field %s references unknown enum %s", e.Name, f.Name, f.EnumName)
			}
		}
		// presentation меняет представление объекта СРАЗУ ВЕЗДЕ — списки,
		// подбор, поиск, REST, DSL. Опечатка в имени реквизита обязана падать
		// здесь: в рантайме она выглядела бы как «представление вдруг стало
		// другим», и искать причину пришлось бы по всему интерфейсу (#846).
		seenPresentation := make(map[string]bool, len(e.Presentation))
		for _, name := range e.Presentation {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("entity %s: presentation содержит пустое имя реквизита", e.Name)
			}
			key := strings.ToLower(name)
			if seenPresentation[key] {
				return fmt.Errorf("entity %s: presentation содержит повтор реквизита %s", e.Name, name)
			}
			seenPresentation[key] = true
			f := findEntityFieldFold(e, name)
			if f == nil {
				return fmt.Errorf("entity %s: presentation ссылается на несуществующий реквизит %s", e.Name, name)
			}
			if f.Type != FieldTypeString {
				return fmt.Errorf("entity %s: presentation реквизит %s должен быть строковым (сейчас %s)", e.Name, name, f.Type)
			}
		}
		if err := validateFieldIDs(e); err != nil {
			return err
		}
		if err := validateTileView(e); err != nil {
			return err
		}
		if err := validateActivity(e); err != nil {
			return err
		}
		if err := validateIndexes(e); err != nil {
			return err
		}
		if err := validateFullText(e); err != nil {
			return err
		}
		if err := validateSearchFields(e); err != nil {
			return err
		}
		if err := validateStages(e, enums); err != nil {
			return err
		}
		if err := validateDetailPanel(e); err != nil {
			return err
		}
		if err := validateNumerator(e); err != nil {
			return err
		}
		if err := validateFormFields(e); err != nil {
			return err
		}
		for _, tp := range e.TableParts {
			for _, f := range tp.Fields {
				if IsRichText(f.Type) {
					return fmt.Errorf("поле %s.%s: тип richtext не поддерживается в табличных частях", tp.Name, f.Name)
				}
				if IsImage(f.Type) {
					return fmt.Errorf("поле %s.%s: тип image не поддерживается в табличных частях", tp.Name, f.Name)
				}
			}
		}
		for _, src := range e.BasedOn {
			if !entityNames[src] {
				return fmt.Errorf("entity %s: based_on references unknown entity %s", e.Name, src)
			}
		}
	}
	return nil
}

// fieldIDPattern ограничивает идентификатор поля латиницей, цифрами и «_»:
// он попадает в служебную таблицу соответствия и в вывод плана миграции, где
// от него нужна однозначность, а не выразительность (имя поля — отдельно).
var fieldIDPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateFieldIDs проверяет устойчивые идентификаторы полей (план 81):
// формат и уникальность в пределах таблицы. Уникальность именно потабличная —
// у шапки и у каждой табличной части своя таблица, поэтому совпадение ID между
// ними безвредно, а внутри одной таблицы означало бы две колонки с одной
// идентичностью.
func validateFieldIDs(e *Entity) error {
	check := func(scope string, fields []Field) error {
		seen := make(map[string]string, len(fields))
		for _, f := range fields {
			if f.ID == "" {
				continue
			}
			if !fieldIDPattern.MatchString(f.ID) {
				return fmt.Errorf("%s: поле %s: id %q — допустимы латиница, цифры и подчёркивание, первый знак не цифра",
					scope, f.Name, f.ID)
			}
			if prev, dup := seen[f.ID]; dup {
				return fmt.Errorf("%s: id %q задан у двух полей (%s и %s) — идентификатор должен быть уникален",
					scope, f.ID, prev, f.Name)
			}
			seen[f.ID] = f.Name
		}
		return nil
	}
	if err := check("entity "+e.Name, e.Fields); err != nil {
		return err
	}
	for _, tp := range e.TableParts {
		if err := check("entity "+e.Name+", табличная часть "+tp.Name, tp.Fields); err != nil {
			return err
		}
	}
	return nil
}

func validateIndexes(e *Entity) error {
	for i, idx := range e.Indexes {
		if len(idx.Fields) == 0 {
			return fmt.Errorf("entity %s: indexes[%d].fields is required", e.Name, i)
		}
		for _, name := range idx.Fields {
			if findEntityField(e, name) == nil {
				return fmt.Errorf("entity %s: index references unknown field %s", e.Name, name)
			}
		}
	}
	return nil
}

func validateActivity(e *Entity) error {
	if e == nil || e.Activity == nil {
		return nil
	}
	if e.Kind != KindCatalog {
		return fmt.Errorf("entity %s: activity is supported only for catalogs", e.Name)
	}
	if e.Activity.Field == "" {
		return fmt.Errorf("entity %s: activity.field is required", e.Name)
	}
	f := findEntityField(e, e.Activity.Field)
	if f == nil {
		return fmt.Errorf("entity %s: activity.field references unknown field %s", e.Name, e.Activity.Field)
	}
	if f.Type != FieldTypeBool || f.RefEntity != "" {
		return fmt.Errorf("entity %s: activity.field %s must have type bool", e.Name, e.Activity.Field)
	}
	switch e.Activity.DefaultScope {
	case "", ActivityScopeActive, ActivityScopeAll:
	default:
		return fmt.Errorf("entity %s: activity.default_scope must be active or all", e.Name)
	}
	return nil
}

func validateTileView(e *Entity) error {
	if e == nil || e.TileView == nil {
		return nil
	}
	if e.TileView.Image != "" {
		f := findEntityField(e, e.TileView.Image)
		if f == nil {
			return fmt.Errorf("entity %s: tile_view.image references unknown field %s", e.Name, e.TileView.Image)
		}
		if !IsImage(f.Type) {
			return fmt.Errorf("entity %s: tile_view.image field %s must have type image", e.Name, e.TileView.Image)
		}
	}
	for _, item := range []struct {
		role string
		name string
	}{
		{"title", e.TileView.Title},
		{"subtitle", e.TileView.Subtitle},
	} {
		if item.name == "" {
			continue
		}
		if findEntityField(e, item.name) == nil {
			return fmt.Errorf("entity %s: tile_view.%s references unknown field %s", e.Name, item.role, item.name)
		}
	}
	for _, name := range e.TileView.Fields {
		if findEntityField(e, name) == nil {
			return fmt.Errorf("entity %s: tile_view.fields references unknown field %s", e.Name, name)
		}
	}
	return nil
}

// validateFullText проверяет блок `fulltext:` (план 82): перечисленные поля
// должны существовать в шапке и нести текст. Ссылочные и перечислимые поля
// хранят UUID/код — индексировать их бессмысленно, а молча пропустить значит
// оставить пользователя с пустой выдачей и без объяснения.
func validateFullText(e *Entity) error {
	if !e.FullTextSet {
		return nil
	}
	seen := make(map[string]bool, len(e.FullText))
	for _, name := range e.FullText {
		f := findEntityFieldFold(e, name)
		if f == nil {
			return fmt.Errorf("entity %s: fulltext ссылается на неизвестный реквизит %s", e.Name, name)
		}
		if f.RefEntity != "" || f.EnumName != "" || (f.Type != FieldTypeString && !IsRichText(f.Type)) {
			return fmt.Errorf("entity %s: реквизит %s нельзя индексировать полнотекстовым поиском — нужен тип string или richtext",
				e.Name, f.Name)
		}
		key := strings.ToLower(f.Name)
		if seen[key] {
			return fmt.Errorf("entity %s: реквизит %s указан в fulltext дважды", e.Name, f.Name)
		}
		seen[key] = true
	}
	return nil
}

// validateSearchFields проверяет блок `search_fields:`: перечисленные реквизиты
// должны существовать в шапке и не быть ссылками. Тип здесь, в отличие от
// fulltext, не ограничен строкой — смысл блока как раз в том, чтобы добавить в
// поиск артикул или штрихкод, которые часто хранят числом.
//
// Ссылка отклоняется: в колонке лежит UUID, поиск подстроки по нему всегда даёт
// пустую выдачу. Молча пропустить такой реквизит значит оставить автора
// конфигурации с неработающим поиском и без объяснения причины.
func validateSearchFields(e *Entity) error {
	if !e.SearchSet {
		return nil
	}
	seen := make(map[string]bool, len(e.Search))
	for _, name := range e.Search {
		f := findEntityFieldFold(e, name)
		if f == nil {
			return fmt.Errorf("entity %s: search_fields ссылается на неизвестный реквизит %s", e.Name, name)
		}
		if f.RefEntity != "" {
			return fmt.Errorf("entity %s: реквизит %s — ссылка, искать по ней подстроку нельзя (в колонке UUID); укажите реквизит справочника-владельца",
				e.Name, f.Name)
		}
		key := strings.ToLower(f.Name)
		if seen[key] {
			return fmt.Errorf("entity %s: реквизит %s указан в search_fields дважды", e.Name, f.Name)
		}
		seen[key] = true
	}
	return nil
}

// validateNumerator проверяет блок `numerator:` (план 117B). У справочника он
// объявляет стандартный «Код», у документа — «Номер»; до 117B блок у
// справочника парсился и МОЛЧА ничего не делал.
func validateNumerator(e *Entity) error {
	n := e.Numerator
	if n == nil {
		return nil
	}
	switch e.Kind {
	case KindCatalog, KindDocument:
	default:
		return fmt.Errorf("entity %s: нумератор применим только к справочникам и документам", e.Name)
	}
	switch strings.ToLower(n.Period) {
	case "none", "year", "month", "day", "":
	default:
		return fmt.Errorf("entity %s: numerator.period = %q — допустимы none, year, month, day", e.Name, n.Period)
	}
	// Сброс счётчика у справочника означал бы выдачу уже занятого кода: код
	// живёт с элементом всю жизнь, а не начинается заново каждый год.
	period := strings.ToLower(n.PeriodOrDefault(e.Kind))
	if e.Kind == KindCatalog && period != "none" {
		return fmt.Errorf("entity %s: numerator.period = %q у справочника — сброс счётчика выдал бы уже занятый код; используйте none", e.Name, n.Period)
	}
	if n.Scope != "" && findEntityFieldFold(e, n.Scope) == nil {
		return fmt.Errorf("entity %s: numerator.scope ссылается на неизвестный реквизит %s", e.Name, n.Scope)
	}
	// Уникальность глобальна по объекту, а счётчик со сбросом выдаёт одно и то
	// же значение в каждом новом периоде: «Р-0001» в 2026-м и «Р-0001» в
	// 2027-м. Такая конфигурация сломалась бы не при обновлении, а первого
	// января — на данных, которые по замыслу верны. Различает периоды маска
	// даты в префиксе, поэтому требуем её, а не запрещаем сочетание.
	if n.Unique && period != "none" && !NumeratorPrefixDistinguishesPeriod(n.Prefix, period) {
		return fmt.Errorf("entity %s: numerator.unique вместе с period = %q требует маску даты в prefix, различающую периоды (например \"{YYYY}{MM}{DD}-\"), иначе счётчик выдаст занятое значение в следующем периоде",
			e.Name, period)
	}
	// Стандартное поле обязано остаться строкой: НайтиПоКоду, представление и
	// обмен работают с текстом, а префикс числовым не бывает.
	std := StandardCodeField
	if e.Kind == KindDocument {
		std = "Номер"
	}
	if f := findEntityFieldFold(e, std); f != nil {
		if f.Type != FieldTypeString {
			return fmt.Errorf("entity %s: при объявленном нумераторе реквизит %s обязан быть строкой (сейчас %s)", e.Name, std, f.Type)
		}
		if f.RefEntity != "" || f.EnumName != "" {
			return fmt.Errorf("entity %s: при объявленном нумераторе реквизит %s не может быть ссылкой или перечислением", e.Name, std)
		}
	}
	return nil
}

// NumeratorPrefixDistinguishesPeriod reports whether the supported date masks
// make values from different counter periods distinct. A monthly counter needs
// both year and month: {MM} alone repeats next year. A daily counter likewise
// needs the complete calendar date.
func NumeratorPrefixDistinguishesPeriod(prefix, period string) bool {
	hasYear := strings.Contains(prefix, "{YYYY}") || strings.Contains(prefix, "{YY}")
	switch strings.ToLower(period) {
	case "", "none":
		return true
	case "year":
		return hasYear
	case "month":
		return hasYear && strings.Contains(prefix, "{MM}")
	case "day":
		return hasYear && strings.Contains(prefix, "{MM}") && strings.Contains(prefix, "{DD}")
	default:
		return false
	}
}

// validateFormFields проверяет `item_form:` и `list_form:`: перечисленные
// реквизиты должны существовать. Опечатка иначе означала бы «реквизит остался
// на форме» — состав задан, а поведение прежнее, и автор ищет причину в коде
// платформы. Тот же довод, что у detail_panel ниже.
//
// Проверка появилась вместе с оживлением `item_form:` (план 117, Д12): пока
// ключ не читал никто, опечатка в нём не значила ничего, и ловить её было
// незачем.
func validateFormFields(e *Entity) error {
	check := func(key string, names []string, allowLegacyTablePartField bool) error {
		for _, name := range names {
			if findEntityFieldFold(e, name) != nil {
				continue
			}
			// Старые версии конфигуратора записывали реквизиты табличных частей
			// как tp.<ТЧ>.<Реквизит>, хотя автоформа всегда показывает ТЧ
			// целиком. Принимаем эти legacy-значения в item_form, чтобы обновление
			// не ломало существующие проекты; новый конфигуратор их не создаёт.
			if allowLegacyTablePartField && findEntityTablePartFieldFold(e, name) {
				continue
			}
			return fmt.Errorf("entity %s: %s ссылается на неизвестный реквизит %s", e.Name, key, name)
		}
		return nil
	}
	if err := check("item_form", e.ItemFormNames(), true); err != nil {
		return err
	}
	return check("list_form", e.ListForm, false)
}

func findEntityTablePartFieldFold(e *Entity, name string) bool {
	parts := strings.SplitN(name, ".", 3)
	if len(parts) != 3 || !strings.EqualFold(parts[0], "tp") {
		return false
	}
	for _, tp := range e.TableParts {
		if !strings.EqualFold(tp.Name, parts[1]) {
			continue
		}
		for _, field := range tp.Fields {
			if strings.EqualFold(field.Name, parts[2]) {
				return true
			}
		}
	}
	return false
}

// validateDetailPanel проверяет блок `detail_panel:`: перечисленные реквизиты
// должны существовать в шапке. Опечатка иначе давала бы пустую строку в панели
// без объяснения — автор искал бы причину в данных, а не в YAML.
//
// Тяжёлые вкладки (табличные части, вложения) отклоняются явно: ключи
// зарезервированы под 118D, и молчаливое игнорирование выглядело бы как
// «объявил, но не работает» — тот самый класс дефектов, что мы чинили.
func validateDetailPanel(e *Entity) error {
	dp := e.DetailPanel
	if dp == nil {
		return nil
	}
	if dp.Width != 0 && (dp.Width < DetailPanelMinWidth || dp.Width > DetailPanelMaxWidth) {
		return fmt.Errorf("entity %s: detail_panel.width должен быть от %d до %d px", e.Name, DetailPanelMinWidth, DetailPanelMaxWidth)
	}
	check := func(where string, names []string) error {
		for _, name := range names {
			if findEntityFieldFold(e, name) == nil {
				return fmt.Errorf("entity %s: detail_panel%s ссылается на неизвестный реквизит %s", e.Name, where, name)
			}
		}
		return nil
	}
	if dp.Title != "" && findEntityFieldFold(e, dp.Title) == nil {
		return fmt.Errorf("entity %s: detail_panel.title ссылается на неизвестный реквизит %s", e.Name, dp.Title)
	}
	if err := check(".fields", dp.Fields); err != nil {
		return err
	}
	tabsConfigured := dp.TabsSet || len(dp.Tabs) > 0
	if dp.FieldsSet && tabsConfigured {
		return fmt.Errorf("entity %s: detail_panel — задайте либо fields, либо tabs, но не оба", e.Name)
	}
	if dp.TabsSet && len(dp.Tabs) == 0 {
		return fmt.Errorf("entity %s: detail_panel.tabs не может быть пустым; для явно пустой панели используйте fields: []", e.Name)
	}
	seen := map[string]bool{}
	for _, tab := range dp.Tabs {
		if strings.TrimSpace(tab.Name) == "" {
			return fmt.Errorf("entity %s: detail_panel.tabs — у закладки нет имени (name)", e.Name)
		}
		key := strings.ToLower(tab.Name)
		if seen[key] {
			return fmt.Errorf("entity %s: detail_panel.tabs — закладка %s объявлена дважды", e.Name, tab.Name)
		}
		seen[key] = true
		if tab.TablePartsSet || tab.AttachmentsSet {
			return fmt.Errorf("entity %s: detail_panel.tabs[%s]: tableparts/attachments зарезервированы для 118D и пока не поддерживаются", e.Name, tab.Name)
		}
		if len(tab.Fields) == 0 {
			return fmt.Errorf("entity %s: detail_panel.tabs[%s].fields не может быть пустым", e.Name, tab.Name)
		}
		if err := check(".tabs["+tab.Name+"].fields", tab.Fields); err != nil {
			return err
		}
	}
	return nil
}

// validateStages проверяет блок `stages:` (план 121). Опечатка в имени этапа
// здесь стоит дороже обычной: гейт переходов отвергает всё, чего нет в
// `transitions`, поэтому незамеченная опечатка означает не «правило не
// сработало», а «объект больше нельзя двигать». Поэтому проверяется и то, что
// формально не мешает загрузиться: недостижимые этапы и переходы из
// необъявленного состояния.
func validateStages(e *Entity, enums []*Enum) error {
	s := e.Stages
	if s == nil {
		return nil
	}
	if s.Field == "" {
		return fmt.Errorf("entity %s: stages без field — укажите реквизит-перечисление, ведущий этапы", e.Name)
	}
	f := findEntityFieldFold(e, s.Field)
	if f == nil {
		return fmt.Errorf("entity %s: stages.field ссылается на неизвестный реквизит %s", e.Name, s.Field)
	}
	if f.EnumName == "" {
		return fmt.Errorf("entity %s: реквизит %s не перечисление — этапы ведутся только по enum-реквизиту", e.Name, f.Name)
	}
	s.Field = f.Name // канонизируем написание: дальше по нему ищут значение в map полей
	if s.Enforce != StageEnforceWarn && s.Enforce != StageEnforceStrict {
		return fmt.Errorf("entity %s: stages.enforce = %q — допустимы %s и %s", e.Name, s.Enforce, StageEnforceWarn, StageEnforceStrict)
	}
	if len(s.Order) == 0 {
		return fmt.Errorf("entity %s: stages без order — порядок этапов задаёт начальный этап, отчёт и схему", e.Name)
	}

	// Значения этапов не дублируются в блоке — они обязаны быть значениями
	// перечисления. Список enums пуст у вызовов, которые проверяют только
	// структуру (как и у проверки типов реквизитов выше).
	var enumValues []string
	for _, en := range enums {
		if strings.EqualFold(en.Name, f.EnumName) {
			enumValues = en.Values
			break
		}
	}
	knownEnumValue := func(v string) bool {
		if len(enums) == 0 {
			return true
		}
		for _, ev := range enumValues {
			if strings.EqualFold(ev, v) {
				return true
			}
		}
		return false
	}

	seen := make(map[string]bool, len(s.Order))
	for _, stage := range s.Order {
		key := strings.ToLower(stage)
		if seen[key] {
			return fmt.Errorf("entity %s: этап %s указан в order дважды", e.Name, stage)
		}
		seen[key] = true
		if !knownEnumValue(stage) {
			return fmt.Errorf("entity %s: этап %s не значение перечисления %s", e.Name, stage, f.EnumName)
		}
	}

	fromSeen := make(map[string]bool, len(s.Transitions))
	adjacent := make(map[string][]string, len(s.Transitions))
	for _, tr := range s.Transitions {
		if !s.Known(tr.From) {
			return fmt.Errorf("entity %s: переход из %s — такого этапа нет в order", e.Name, tr.From)
		}
		key := strings.ToLower(tr.From)
		if fromSeen[key] {
			return fmt.Errorf("entity %s: переходы из этапа %s заданы дважды — объедините их в один список to", e.Name, tr.From)
		}
		fromSeen[key] = true
		if len(tr.To) == 0 {
			return fmt.Errorf("entity %s: переход из %s без to — уберите строку или перечислите этапы", e.Name, tr.From)
		}
		toSeen := make(map[string]bool, len(tr.To))
		for _, to := range tr.To {
			if !s.Known(to) {
				return fmt.Errorf("entity %s: переход %s → %s — такого этапа нет в order", e.Name, tr.From, to)
			}
			tk := strings.ToLower(to)
			if toSeen[tk] {
				return fmt.Errorf("entity %s: переход %s → %s указан дважды", e.Name, tr.From, to)
			}
			toSeen[tk] = true
			adjacent[key] = append(adjacent[key], tk)
		}
	}
	// Достижимость — это путь ИЗ начального этапа, а не просто наличие любой
	// входящей дуги. Иначе изолированный цикл C↔D ошибочно считался достижимым:
	// каждый его узел имеет вход, но попасть в цикл из начала маршрута нельзя.
	reachable := make(map[string]bool, len(s.Order))
	initial := strings.ToLower(s.Initial())
	reachable[initial] = true
	queue := []string{initial}
	for len(queue) > 0 {
		from := queue[0]
		queue = queue[1:]
		for _, to := range adjacent[from] {
			if reachable[to] {
				continue
			}
			reachable[to] = true
			queue = append(queue, to)
		}
	}
	for _, stage := range s.Order {
		if !reachable[strings.ToLower(stage)] {
			return fmt.Errorf("entity %s: этап %s недостижим — в него не ведёт ни один переход, и он не начальный (%s)",
				e.Name, stage, s.Initial())
		}
	}

	for stage := range s.DeadlineDays {
		if !s.Known(stage) {
			return fmt.Errorf("entity %s: deadline_days задан для неизвестного этапа %s", e.Name, stage)
		}
		if s.DeadlineDays[stage] <= 0 {
			return fmt.Errorf("entity %s: deadline_days[%s] = %d — срок задаётся положительным числом дней",
				e.Name, stage, s.DeadlineDays[stage])
		}
	}
	return nil
}

func findEntityField(e *Entity, name string) *Field {
	for i := range e.Fields {
		if e.Fields[i].Name == name {
			return &e.Fields[i]
		}
	}
	return nil
}
