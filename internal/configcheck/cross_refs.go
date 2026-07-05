package configcheck

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/project"
)

// nameSet — множество имён объектов конфигурации (сравнение регистронезависимо,
// т.к. идентификаторы в OneBase case-insensitive).
type nameSet map[string]bool

func (s nameSet) add(n string)      { s[strings.ToLower(n)] = true }
func (s nameSet) has(n string) bool { return s[strings.ToLower(strings.TrimSpace(n))] }

// CheckCrossRefs проверяет, что ссылки между объектами указывают на
// существующие цели: документы в журналах/подсистемах/ролях, виджеты на
// главной странице, источник печатной формы, права ролей. Это ловит опечатки
// в именах, которые компиляция запросов не видит (объект просто не подключится).
func CheckCrossRefs(proj *project.Project, roles []*auth.Role) []Issue {
	docs := nameSet{}
	cats := nameSet{}
	entityByName := map[string]*metadata.Entity{}
	for _, e := range proj.Entities {
		entityByName[strings.ToLower(e.Name)] = e
		switch e.Kind {
		case metadata.KindDocument:
			docs.add(e.Name)
		case metadata.KindCatalog:
			cats.add(e.Name)
		}
	}
	reports := nameSet{}
	for _, r := range proj.Reports {
		reports.add(r.Name)
	}
	widgets := nameSet{}
	for _, w := range proj.Widgets {
		widgets.add(w.Name)
	}
	inforegs := nameSet{}
	for _, ir := range proj.InfoRegisters {
		inforegs.add(ir.Name)
	}
	// Регистры для ролей/подсистем: накопления + бухгалтерии (на оба ссылаются
	// в разделе registers).
	registers := nameSet{}
	for _, r := range proj.Registers {
		registers.add(r.Name)
	}
	for _, ar := range proj.AccountRegisters {
		registers.add(ar.Name)
	}
	processors := nameSet{}
	for _, p := range proj.Processors {
		processors.add(p.Name)
	}
	journals := nameSet{}
	for _, j := range proj.Journals {
		journals.add(j.Name)
	}

	var issues []Issue
	add := func(file, object, kind, msg string) {
		issues = append(issues, Issue{File: file, Object: object, Kind: kind, Message: msg})
	}
	// checkRefs проверяет список ссылок против набора; what — что за ссылка.
	checkRefs := func(file, object, kind string, refs []string, set nameSet, what string) {
		for _, r := range refs {
			if r != "" && !set.has(r) {
				add(file, object, kind, fmt.Sprintf("%s %q не найден(а)", what, r))
			}
		}
	}

	// Журналы → документы + поля колонок/фильтров.
	for _, j := range proj.Journals {
		checkRefs("journals", j.Name, "Журнал", j.Documents, docs, "документ")
		// Документы журнала, которые реально существуют — по ним резолвим поля.
		var jdocs []*metadata.Entity
		for _, dn := range j.Documents {
			if e := entityByName[strings.ToLower(dn)]; e != nil {
				jdocs = append(jdocs, e)
			}
		}
		if len(jdocs) == 0 {
			continue
		}
		// Колонка/фильтр должны резолвиться в поле хотя бы одного документа
		// (через Map/точное имя/Fallback). Тип документа журнал показывает сам
		// (колонка _doc_kind), его в columns указывать не нужно.
		for _, c := range j.Columns {
			if !journalFieldResolves(c.Field, c.Map, c.Fallback, jdocs) {
				add("journals", j.Name, "Журнал", fmt.Sprintf("колонка %q не найдена ни в одном документе журнала", c.Field))
			}
		}
		for _, f := range j.Filters {
			if !journalFieldResolves(f.Field, nil, nil, jdocs) {
				add("journals", j.Name, "Журнал", fmt.Sprintf("фильтр %q не найден ни в одном документе журнала", f.Field))
			}
		}
		for _, cr := range j.Conditional {
			if cr.Field != "" && !journalOutputFieldExists(j, cr.Field) {
				add("journals", j.Name, "Журнал", fmt.Sprintf("условное оформление: поле %q не является колонкой журнала", cr.Field))
			}
		}
	}

	// Управляемые формы → цели и поля условного оформления табличных частей.
	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			if form == nil || !form.IsManaged() || len(form.Conditional) == 0 {
				continue
			}
			checkFormConditionalRefs(ent, form, add)
		}
	}

	// Подсистемы → объекты в contents.
	for _, s := range proj.Subsystems {
		c := s.Contents
		checkRefs("subsystems", s.Name, "Подсистема", c.Documents, docs, "документ")
		checkRefs("subsystems", s.Name, "Подсистема", c.Catalogs, cats, "справочник")
		checkRefs("subsystems", s.Name, "Подсистема", c.Reports, reports, "отчёт")
		checkRefs("subsystems", s.Name, "Подсистема", c.InfoRegs, inforegs, "регистр сведений")
		checkRefs("subsystems", s.Name, "Подсистема", c.Registers, registers, "регистр")
		checkRefs("subsystems", s.Name, "Подсистема", c.Processors, processors, "обработка")
		checkRefs("subsystems", s.Name, "Подсистема", c.Journals, journals, "журнал")
	}

	// Главная страница (глобальная и подсистемные) → виджеты.
	checkHomePageWidgets := func(file, object string, hp *metadata.HomePage) {
		if hp == nil {
			return
		}
		for _, row := range hp.Rows {
			checkRefs(file, object, "Главная страница", row.Widgets, widgets, "виджет")
		}
		for _, w := range hp.Widgets {
			if w.Name != "" && !widgets.has(w.Name) {
				add(file, object, "Главная страница", fmt.Sprintf("виджет %q не найден", w.Name))
			}
		}
	}
	checkHomePageWidgets("config/home_page.yaml", "home_page", proj.HomePage)
	for _, s := range proj.Subsystems {
		checkHomePageWidgets("subsystems", s.Name, s.HomePage)
	}

	// Главная страница → блок nav (опциональное ограничение левого меню).
	if proj.HomePage != nil && proj.HomePage.Nav != nil {
		c := proj.HomePage.Nav
		checkRefs("config/home_page.yaml", "home_page.nav", "Главная страница", c.Documents, docs, "документ")
		checkRefs("config/home_page.yaml", "home_page.nav", "Главная страница", c.Catalogs, cats, "справочник")
		checkRefs("config/home_page.yaml", "home_page.nav", "Главная страница", c.Reports, reports, "отчёт")
		checkRefs("config/home_page.yaml", "home_page.nav", "Главная страница", c.InfoRegs, inforegs, "регистр сведений")
		checkRefs("config/home_page.yaml", "home_page.nav", "Главная страница", c.Registers, registers, "регистр")
		checkRefs("config/home_page.yaml", "home_page.nav", "Главная страница", c.Processors, processors, "обработка")
		checkRefs("config/home_page.yaml", "home_page.nav", "Главная страница", c.Journals, journals, "журнал")
	}

	// Печатные формы → документ/справочник-источник и табличная часть.
	for _, pf := range proj.PrintForms {
		// «general» — зарезервированный источник для форм без привязки к
		// конкретному документу (сводные отчёты, рендерятся программно из
		// переданного контекста). Источник и table.source не проверяем.
		if strings.EqualFold(pf.Document, "general") {
			continue
		}
		if pf.Document != "" && !docs.has(pf.Document) && !cats.has(pf.Document) {
			add("printforms", pf.Name, "Печатная форма", fmt.Sprintf("источник %q не найден среди документов и справочников", pf.Document))
		}
		if pf.Table != nil && pf.Table.Source != "" {
			if e := entityByName[strings.ToLower(pf.Document)]; e != nil {
				var tp *metadata.TablePart
				for i := range e.TableParts {
					if strings.EqualFold(e.TableParts[i].Name, pf.Table.Source) {
						tp = &e.TableParts[i]
						break
					}
				}
				if tp == nil {
					add("printforms", pf.Name, "Печатная форма", fmt.Sprintf("табличная часть %q не найдена в %q", pf.Table.Source, pf.Document))
				} else {
					// Колонки и итоги таблицы должны ссылаться на поля ТЧ.
					for _, c := range pf.Table.Columns {
						if !tpFieldExists(c.Field, tp) {
							add("printforms", pf.Name, "Печатная форма", fmt.Sprintf("колонка %q не найдена в табличной части %q", c.Field, pf.Table.Source))
						}
					}
					for _, tot := range pf.Table.Totals {
						if !tpFieldExists(tot.Field, tp) {
							add("printforms", pf.Name, "Печатная форма", fmt.Sprintf("итог по %q: поле не найдено в табличной части %q", tot.Field, pf.Table.Source))
						}
					}
				}
			}
		}
	}

	// Печатные формы v2 (макеты .layout.yaml) — валидация binding.
	for _, lf := range proj.LayoutForms {
		issues = append(issues, checkLayoutForm(lf, docs, cats, entityByName)...)
	}

	// Регистры бухгалтерии → типы субконто ссылаются на существующие сущности.
	// Ловит опечатки вида reference:НесуществующийСправочник в блоке subconto,
	// которые иначе всплыли бы только при проведении (no such column / битый JOIN).
	for _, ar := range proj.AccountRegisters {
		for _, s := range ar.Subconto {
			if metadata.IsReference(s.Type) {
				target := metadata.RefName(s.Type)
				if !cats.has(target) && !docs.has(target) {
					add("accountregs", ar.Name, "Регистр бухгалтерии",
						fmt.Sprintf("субконто %q ссылается на несуществующую сущность %q", s.Name, target))
				}
			}
		}
	}

	// Роли → объекты в правах.
	for _, r := range roles {
		checkRefs("roles", r.Name, "Роль", keys(r.Permissions.Catalogs), cats, "справочник")
		checkRefs("roles", r.Name, "Роль", keys(r.Permissions.Documents), docs, "документ")
		checkRefs("roles", r.Name, "Роль", keys(r.Permissions.Registers), registers, "регистр")
		checkRefs("roles", r.Name, "Роль", keys(r.Permissions.InfoRegs), inforegs, "регистр сведений")
		checkRefs("roles", r.Name, "Роль", keys(r.Permissions.Reports), reports, "отчёт")
		checkRefs("roles", r.Name, "Роль", keys(r.Permissions.Processors), processors, "обработка")
	}

	return issues
}

// CheckNameCollisions ловит совпадение имён сущностей (справочников и
// документов) без учёта регистра. Имя физической таблицы строится как
// metadata.TableName(name) = lower(name) без префикса вида, поэтому каталог
// «Счёт» и документ «Счёт» претендуют на одну таблицу «счёт» — миграция тихо
// смешает их колонки. Регистры (рег_/инфо_) и табличные части (<сущность>_<тч>)
// имеют префиксы и в этом пространстве имён не участвуют. См. issue #20.
func CheckNameCollisions(proj *project.Project) []Issue {
	type ent struct {
		name string
		kind string
	}
	byTable := map[string][]ent{}
	kindLabel := func(k metadata.Kind) string {
		switch k {
		case metadata.KindCatalog:
			return "справочник"
		case metadata.KindDocument:
			return "документ"
		default:
			return string(k)
		}
	}
	for _, e := range proj.Entities {
		tbl := metadata.TableName(e.Name)
		byTable[tbl] = append(byTable[tbl], ent{name: e.Name, kind: kindLabel(e.Kind)})
	}

	var tables []string
	for tbl, group := range byTable {
		if len(group) > 1 {
			tables = append(tables, tbl)
		}
	}
	sort.Strings(tables)

	var issues []Issue
	for _, tbl := range tables {
		group := byTable[tbl]
		sort.Slice(group, func(i, j int) bool { return group[i].name < group[j].name })
		var parts []string
		for _, g := range group {
			parts = append(parts, fmt.Sprintf("%s %q", g.kind, g.name))
		}
		issues = append(issues, Issue{
			Object: tbl,
			Kind:   "Имя таблицы",
			Message: fmt.Sprintf(
				"коллизия имён: %s используют одну таблицу %q — переименуйте один из объектов",
				strings.Join(parts, ", "), tbl),
		})
	}
	return issues
}

// fieldInEntity сообщает, есть ли у сущности поле с таким именем.
func fieldInEntity(e *metadata.Entity, name string) bool {
	for i := range e.Fields {
		if strings.EqualFold(e.Fields[i].Name, name) {
			return true
		}
	}
	return false
}

// journalFieldResolves повторяет логику colExprForDoc: колонка/фильтр журнала
// валидна, если резолвится в реальное поле хотя бы одного документа — через
// явный Map (docName→field), точное имя или Fallback.
func journalFieldResolves(field string, fieldMap map[string]string, fallback []string, docs []*metadata.Entity) bool {
	for _, e := range docs {
		if fieldMap != nil {
			if mapped, ok := fieldMap[e.Name]; ok {
				if fieldInEntity(e, mapped) {
					return true
				}
				continue // Map задан, но указывает в этом документе на пустоту
			}
		}
		if fieldInEntity(e, field) {
			return true
		}
		for _, fb := range fallback {
			if fieldInEntity(e, fb) {
				return true
			}
		}
	}
	return false
}

func journalOutputFieldExists(j *metadata.Journal, field string) bool {
	if field == "" || isJournalDocKindField(field) {
		return true
	}
	for _, c := range j.Columns {
		if strings.EqualFold(c.Field, field) {
			return true
		}
	}
	return false
}

func isJournalDocKindField(field string) bool {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "_doc_kind", "документ", "document":
		return true
	default:
		return false
	}
}

// tpFieldExists проверяет, что поле таблицы печатной формы существует в
// табличной части. «@row» — служебный псевдостолбец (номер строки). Точечные
// ссылки (Поле.Реквизит) проверяются по корню.
func tpFieldExists(field string, tp *metadata.TablePart) bool {
	if field == "" || strings.HasPrefix(field, "@") {
		return true
	}
	root := field
	if i := strings.IndexByte(root, '.'); i >= 0 {
		root = root[:i]
	}
	for i := range tp.Fields {
		if strings.EqualFold(tp.Fields[i].Name, root) {
			return true
		}
	}
	return false
}

type formCondTarget struct {
	name   string
	fields map[string]bool
}

func checkFormConditionalRefs(ent *metadata.Entity, form *metadata.FormModule, add func(file, object, kind, msg string)) {
	targets := formConditionalRefTargets(ent, form)
	file := formFileLabel(ent, form)
	for _, cr := range form.Conditional {
		targetName := strings.TrimSpace(cr.Target)
		fieldName := strings.TrimSpace(cr.Field)
		if targetName == "" {
			if fieldName == "" {
				continue
			}
			found := false
			for _, target := range targets {
				if target.fields[strings.ToLower(fieldName)] {
					found = true
					break
				}
			}
			if !found {
				add(file, form.Name, "Управляемая форма", fmt.Sprintf("условное оформление: поле %q не найдено ни в одной табличной части формы", fieldName))
			}
			continue
		}
		target := targets[strings.ToLower(targetName)]
		if target.name == "" {
			add(file, form.Name, "Управляемая форма", fmt.Sprintf("условное оформление: цель %q не найдена среди табличных частей формы", targetName))
			continue
		}
		if fieldName != "" && !target.fields[strings.ToLower(fieldName)] {
			add(file, form.Name, "Управляемая форма", fmt.Sprintf("условное оформление: поле %q не найдено в табличной части %q", fieldName, target.name))
		}
	}
}

func formConditionalRefTargets(ent *metadata.Entity, form *metadata.FormModule) map[string]formCondTarget {
	targets := map[string]formCondTarget{}
	addTarget := func(alias, name string, fields []string) {
		alias = strings.TrimSpace(alias)
		name = strings.TrimSpace(name)
		if alias == "" || name == "" {
			return
		}
		set := map[string]bool{}
		for _, f := range fields {
			if f != "" {
				set[strings.ToLower(f)] = true
			}
		}
		targets[strings.ToLower(alias)] = formCondTarget{name: name, fields: set}
	}
	if ent != nil {
		for i := range ent.TableParts {
			tp := &ent.TableParts[i]
			fields := make([]string, 0, len(tp.Fields))
			for _, f := range tp.Fields {
				fields = append(fields, f.Name)
			}
			addTarget(tp.Name, tp.Name, fields)
		}
	}
	if form != nil {
		for _, attr := range form.Attributes {
			if attr == nil || !strings.EqualFold(attr.TypeRef, "ValueTable") {
				continue
			}
			fields := make([]string, 0, len(attr.Columns))
			for _, c := range attr.Columns {
				if c != nil {
					fields = append(fields, c.Name)
				}
			}
			addTarget(attr.Name, attr.Name, fields)
		}
		form.Walk(func(el *metadata.FormElement) bool {
			if el == nil || el.Kind != metadata.FormElementTablePart {
				return true
			}
			name := formDataPathField(el.DataPath)
			if name == "" {
				name = el.TablePart
			}
			if name == "" {
				name = el.Name
			}
			if target := targets[strings.ToLower(name)]; target.name != "" {
				addTarget(el.Name, target.name, targetFieldNames(target))
			}
			return true
		})
	}
	return targets
}

func targetFieldNames(target formCondTarget) []string {
	out := make([]string, 0, len(target.fields))
	for f := range target.fields {
		out = append(out, f)
	}
	return out
}

func formDataPathField(path string) string {
	path = strings.TrimSpace(path)
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}

// checkLayoutForm валидирует binding декларативной печатной формы v2 (макет
// .layout.yaml). Проверяет: существование документа-источника; имена областей в
// sequence/repeat/repeat_header присутствуют в areas; источник repeat — реальная
// ТЧ сущности; выражения параметров ссылаются на существующие поля (насколько
// позволяет инфраструктура — root проверяется в нужном контексте); ячейка-
// параметр без записи в binding и без одноимённого поля — предупреждение.
func checkLayoutForm(lf *printform.LayoutForm, docs, cats nameSet, entityByName map[string]*metadata.Entity) []Issue {
	var issues []Issue
	if lf == nil || lf.Layout == nil {
		return nil
	}
	lt := lf.Layout
	label := "printforms/" + lf.Name + ".layout.yaml"
	add := func(msg string) {
		issues = append(issues, Issue{File: label, Object: lf.Name, Kind: "Печатная форма", Message: msg})
	}

	// «general» — сводные формы без привязки к сущности: контекст не проверяем.
	if strings.EqualFold(lt.Document, "general") {
		return issues
	}

	// Документ-источник существует.
	var entity *metadata.Entity
	if lt.Document != "" {
		entity = entityByName[strings.ToLower(lt.Document)]
		if !docs.has(lt.Document) && !cats.has(lt.Document) {
			add(fmt.Sprintf("источник %q не найден среди документов и справочников", lt.Document))
		}
	}

	// Множество имён областей.
	areaNames := nameSet{}
	for _, a := range lt.Areas {
		areaNames.add(a.Name)
	}

	binding := lt.Binding
	if binding == nil {
		return issues
	}

	// sequence → область существует.
	for _, name := range binding.Sequence {
		if !areaNames.has(name) {
			add(fmt.Sprintf("в sequence указана несуществующая область %q", name))
		}
	}
	// repeat_header → область существует.
	if binding.RepeatHeader != "" && !areaNames.has(binding.RepeatHeader) {
		add(fmt.Sprintf("repeat_header ссылается на несуществующую область %q", binding.RepeatHeader))
	}

	// repeat → область + источник-ТЧ существуют; параметры резолвятся в поля ТЧ.
	repeatByArea := map[string]*printform.RepeatBinding{}
	for i := range binding.Repeat {
		rb := &binding.Repeat[i]
		if !areaNames.has(rb.Area) {
			add(fmt.Sprintf("repeat ссылается на несуществующую область %q", rb.Area))
		}
		repeatByArea[strings.ToLower(rb.Area)] = rb

		var tp *metadata.TablePart
		if entity != nil && rb.Source != "" {
			tp = findTablePart(entity, rb.Source)
			if tp == nil {
				add(fmt.Sprintf("repeat области %q: табличная часть %q не найдена в %q", rb.Area, rb.Source, lt.Document))
			}
		}
		// Параметры repeat — root-поле в ТЧ (или @row/Итог/Константы/ссылка).
		for _, expr := range rb.Parameters {
			if tp != nil {
				if bad := badExprForTP(expr, tp); bad != "" {
					add(fmt.Sprintf("repeat области %q: параметр-выражение ссылается на несуществующее поле %q табличной части %q", rb.Area, bad, rb.Source))
				}
			}
		}
	}

	// Параметры контекста документа (binding.parameters) — root-поле в сущности.
	for _, expr := range binding.Parameters {
		if entity != nil {
			if bad := badExprForEntity(expr, entity); bad != "" {
				add(fmt.Sprintf("параметр-выражение ссылается на несуществующее поле %q документа %q", bad, lt.Document))
			}
		}
	}

	// Ячейки-параметры без записи в binding и без одноимённого поля — сирота.
	for _, area := range lt.Areas {
		rb := repeatByArea[strings.ToLower(area.Name)]
		for _, row := range area.Rows {
			for _, cell := range row.Cells {
				if cell.Parameter == "" {
					continue
				}
				if layoutParamBound(cell.Parameter, rb, binding, entity) {
					continue
				}
				add(fmt.Sprintf("область %q: ячейка-параметр %q не имеет записи в binding и не совпадает с полем — останется пустой", area.Name, cell.Parameter))
			}
		}
	}

	return issues
}

// layoutParamBound сообщает, привязан ли именованный параметр ячейки: есть запись
// в repeat.parameters (для repeat-области) либо в binding.parameters, либо имя
// совпадает с полем сущности (автопривязка по одноимённому полю).
func layoutParamBound(name string, rb *printform.RepeatBinding, binding *printform.Binding, entity *metadata.Entity) bool {
	if rb != nil && hasParamKey(rb.Parameters, name) {
		return true
	}
	if binding != nil && hasParamKey(binding.Parameters, name) {
		return true
	}
	if entity != nil && fieldInEntity(entity, name) {
		return true
	}
	return false
}

// CheckLayoutWarnings собирает НЕблокирующие предупреждения по декларативным
// макетам печатных форм v2. Сейчас — единственное: область, повторяемая по
// строкам табличной части (binding.repeat), содержит ячейку с rowspan>1.
//
// Почему предупреждение, а не ошибка: PDF-рендер (sheet/pdf.go) в MVP не умеет
// корректно разрывать rowspan-ячейку через границу страницы — при попадании
// высокого объединения на разрыв оно рисуется за нижнее поле и накладывается на
// шапку следующей страницы. Для repeat-области (тиражируется на каждую строку
// ТЧ — десятки/сотни строк) это почти гарантированно случится на длинном
// документе. Одноразовые области (шапка/итог) в начале/конце на разрыв почти не
// попадают, поэтому здесь не предупреждаем (меньше шума). Полный перенос
// rowspan через страницу — follow-up плана 64.
func CheckLayoutWarnings(proj *project.Project) []Issue {
	var warns []Issue
	for _, lf := range proj.LayoutForms {
		if lf == nil || lf.Layout == nil || lf.Layout.Binding == nil {
			continue
		}
		lt := lf.Layout
		for i := range lt.Binding.Repeat {
			rb := &lt.Binding.Repeat[i]
			area := lt.Area(rb.Area)
			if area == nil {
				continue // несуществующая область — это уже ошибка checkLayoutForm
			}
			if areaHasRowSpan(area) {
				warns = append(warns, Issue{
					File:   "printforms/" + lf.Name + ".layout.yaml",
					Object: lf.Name,
					Kind:   "Печатная форма",
					Message: fmt.Sprintf("область %q (повтор по ТЧ %q) содержит объединение по строкам (rowspan): "+
						"при печати в PDF такое объединение может некорректно разрываться по страницам", rb.Area, rb.Source),
				})
			}
		}
	}
	return warns
}

// areaHasRowSpan сообщает, есть ли в области ячейка с rowspan>1.
func areaHasRowSpan(area *printform.LayoutArea) bool {
	for _, row := range area.Rows {
		for _, cell := range row.Cells {
			if cell.RowSpan > 1 {
				return true
			}
		}
	}
	return false
}

// hasParamKey ищет ключ параметра регистронезависимо.
func hasParamKey(params map[string]string, name string) bool {
	if params == nil {
		return false
	}
	if _, ok := params[name]; ok {
		return true
	}
	for k := range params {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// findTablePart ищет ТЧ сущности по имени (регистронезависимо).
func findTablePart(e *metadata.Entity, name string) *metadata.TablePart {
	for i := range e.TableParts {
		if strings.EqualFold(e.TableParts[i].Name, name) {
			return &e.TableParts[i]
		}
	}
	return nil
}

// exprRoot выделяет корневое имя поля из выражения «поле[.подполе] | формат».
// Возвращает ("", служебное) для @row/Итог.*/Константы.* — их не проверяем.
func exprRoot(expr string) (root string, skip bool) {
	expr = strings.TrimSpace(expr)
	if i := strings.IndexByte(expr, '|'); i >= 0 {
		expr = strings.TrimSpace(expr[:i])
	}
	if expr == "" || strings.HasPrefix(expr, "@") {
		return "", true
	}
	low := strings.ToLower(expr)
	if strings.HasPrefix(low, "итог.") || strings.HasPrefix(low, "константы.") {
		return "", true
	}
	root = expr
	if i := strings.IndexByte(root, '.'); i >= 0 {
		root = root[:i]
	}
	return root, false
}

// badExprForTP возвращает корень выражения, если он НЕ резолвится в поле ТЧ
// (иначе пустую строку). Служебные выражения (@row/Итог/Константы) валидны.
func badExprForTP(expr string, tp *metadata.TablePart) string {
	root, skip := exprRoot(expr)
	if skip {
		return ""
	}
	for i := range tp.Fields {
		if strings.EqualFold(tp.Fields[i].Name, root) {
			return ""
		}
	}
	return root
}

// badExprForEntity возвращает корень выражения, если он НЕ резолвится в поле
// сущности (иначе пустую строку). Служебные выражения валидны.
func badExprForEntity(expr string, e *metadata.Entity) string {
	root, skip := exprRoot(expr)
	if skip {
		return ""
	}
	if fieldInEntity(e, root) {
		return ""
	}
	return root
}

// keys возвращает отсортированные ключи map прав (детерминированный порядок
// сообщений).
func keys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
