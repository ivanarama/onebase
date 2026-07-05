package configcheck

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckCrossRefs должен ловить ссылки на несуществующие объекты (документы в
// журналах, виджеты на главной, права ролей) и не падать на легитимных случаях
// (печатная форма с document: general).
func TestCheckCrossRefs(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "заказ.yaml"), `name: Заказ
fields:
  - name: Наименование
    type: string`)
	// журнал ссылается на несуществующий документ
	mkFile(t, filepath.Join(dir, "journals", "ж.yaml"), `name: Ж
documents: [Заказ, НетТакого]
columns:
  - field: Наименование`)
	// печатная форма с general — не должна давать ошибку источника
	mkFile(t, filepath.Join(dir, "printforms", "общая.yaml"), `name: Общая
document: general
title: Отчёт
header: "x"`)
	// роль ссылается на несуществующий справочник
	mkFile(t, filepath.Join(dir, "roles", "р.yaml"), `name: Р
permissions:
  catalogs:
    НетТакогоСправочника: [read]`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()
	roles, _ := auth.LoadRolesYAML(filepath.Join(dir, "roles"))

	issues := CheckCrossRefs(proj, roles)

	var hasJournal, hasRole, hasGeneral bool
	for _, i := range issues {
		if i.Kind == "Журнал" && strings.Contains(i.Message, "НетТакого") {
			hasJournal = true
		}
		if i.Kind == "Роль" && strings.Contains(i.Message, "НетТакогоСправочника") {
			hasRole = true
		}
		if i.Object == "Общая" {
			hasGeneral = true
		}
	}
	if !hasJournal {
		t.Errorf("ожидалась ошибка журнала о несуществующем документе: %+v", issues)
	}
	if !hasRole {
		t.Errorf("ожидалась ошибка роли о несуществующем справочнике: %+v", issues)
	}
	if hasGeneral {
		t.Errorf("печатная форма с document: general не должна давать ошибку: %+v", issues)
	}
}

// Проверка полей: колонки журнала, не резолвящиеся ни в одном документе, и поля
// таблицы печатной формы, отсутствующие в табличной части.
func TestCheckCrossRefs_Fields(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "реализация.yaml"), `name: Реализация
fields:
  - name: Дата
    type: date
  - name: Покупатель
    type: reference:Контрагент
tableparts:
  - name: Товары
    fields:
      - name: Номенклатура
        type: reference:Номенклатура
      - name: Сумма
        type: number`)
	mkFile(t, filepath.Join(dir, "catalogs", "контрагент.yaml"), `name: Контрагент
fields:
  - name: Наименование
    type: string`)
	// журнал: Дата резолвится, НетПоля — нет
	mkFile(t, filepath.Join(dir, "journals", "ж.yaml"), `name: Ж
documents: [Реализация]
columns:
  - field: Дата
  - field: НетПоля
conditional:
  - when: Дата <> ""
    field: Документ
  - when: Дата <> ""
    field: НетКолонкиУсловие`)
	// печатная форма: Сумма есть в ТЧ, НетКолонки — нет; @row допустимо
	mkFile(t, filepath.Join(dir, "printforms", "накладная.yaml"), `name: Накладная
document: Реализация
table:
  source: Товары
  columns:
    - field: "@row"
    - field: Сумма
    - field: НетКолонки
  totals:
    - field: Сумма
      sum: true`)
	mkFile(t, filepath.Join(dir, "forms", "реализация", "ФормаОбъекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Реализация
elements:
  - kind: ТабличнаяЧасть
    name: ТаблицаТовары
    data_path: Объект.Товары
conditional:
  - target: ТаблицаТовары
    when: Сумма > 0
    field: Сумма
    then: { background: "#eef" }
  - target: ТаблицаТовары
    when: Сумма > 0
    field: НетКолонкиФормы
    then: { background: "#fee" }
  - target: НетТакойТЧ
    when: Сумма > 0
    then: { background: "#fee" }
`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	issues := CheckCrossRefs(proj, nil)
	var jBad, jGood, jCondBad, jCondGood, pfBad, pfGood, pfRow, formBadTarget, formBadField, formGood bool
	for _, i := range issues {
		switch {
		case i.Kind == "Журнал" && strings.Contains(i.Message, "НетПоля"):
			jBad = true
		case i.Kind == "Журнал" && strings.Contains(i.Message, "НетКолонкиУсловие"):
			jCondBad = true
		case i.Kind == "Журнал" && strings.Contains(i.Message, "Документ"):
			jCondGood = true
		case i.Kind == "Журнал" && strings.Contains(i.Message, `"Дата"`):
			jGood = true
		case i.Kind == "Печатная форма" && strings.Contains(i.Message, "НетКолонки"):
			pfBad = true
		case i.Kind == "Печатная форма" && strings.Contains(i.Message, `"Сумма"`):
			pfGood = true
		case i.Kind == "Печатная форма" && strings.Contains(i.Message, "@row"):
			pfRow = true
		case i.Kind == "Управляемая форма" && strings.Contains(i.Message, "НетТакойТЧ"):
			formBadTarget = true
		case i.Kind == "Управляемая форма" && strings.Contains(i.Message, "НетКолонкиФормы"):
			formBadField = true
		case i.Kind == "Управляемая форма" && strings.Contains(i.Message, `"Сумма"`):
			formGood = true
		}
	}
	if !jBad {
		t.Errorf("ожидалась ошибка журнала о колонке НетПоля: %+v", issues)
	}
	if jGood {
		t.Errorf("колонка Дата резолвится — ошибки быть не должно: %+v", issues)
	}
	if !jCondBad {
		t.Errorf("ожидалась ошибка журнала о поле условного оформления: %+v", issues)
	}
	if jCondGood {
		t.Errorf("служебная колонка Документ валидна — ошибки быть не должно: %+v", issues)
	}
	if !pfBad {
		t.Errorf("ожидалась ошибка печатной формы о колонке НетКолонки: %+v", issues)
	}
	if pfGood || pfRow {
		t.Errorf("Сумма и @row валидны — ошибки быть не должно: %+v", issues)
	}
	if !formBadTarget {
		t.Errorf("ожидалась ошибка формы о цели НетТакойТЧ: %+v", issues)
	}
	if !formBadField {
		t.Errorf("ожидалась ошибка формы о колонке НетКолонкиФормы: %+v", issues)
	}
	if formGood {
		t.Errorf("колонка Сумма в форме валидна — ошибки быть не должно: %+v", issues)
	}
}

// Валидация binding макета v2 (план 64, этап 4.6): источник repeat — реальная
// ТЧ; имена областей в sequence/repeat существуют; выражения параметров ссылаются
// на существующие поля; ячейка-параметр без записи в binding и без одноимённого
// поля — предупреждение.
func TestCheckCrossRefs_LayoutBinding(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "реализация.yaml"), `name: Реализация
fields:
  - name: Номер
    type: string
tableparts:
  - name: Товары
    fields:
      - name: Сумма
        type: number`)
	// Хорошая форма: всё резолвится.
	mkFile(t, filepath.Join(dir, "printforms", "хорошая.layout.yaml"), `name: Хорошая
document: Реализация
areas:
  - name: Заголовок
    rows:
      - cells:
          - text: "Накладная № {{Номер}}"
  - name: Строка
    rows:
      - cells:
          - parameter: С
binding:
  sequence: [Заголовок, Строка]
  repeat:
    - area: Строка
      source: Товары
      parameters:
        С: Сумма | number:2
`)
	// Плохая форма: source-ТЧ нет, область в sequence не существует,
	// ячейка-параметр без binding и без поля.
	mkFile(t, filepath.Join(dir, "printforms", "плохая.layout.yaml"), `name: Плохая
document: Реализация
areas:
  - name: Заголовок
    rows:
      - cells:
          - parameter: Сирота
  - name: Строка
    rows:
      - cells:
          - parameter: С
binding:
  sequence: [Заголовок, НетТакойОбласти]
  repeat:
    - area: Строка
      source: НетТакойТЧ
      parameters:
        С: Сумма
`)
	// Форма с валидной ТЧ, но параметром на несуществующее поле.
	mkFile(t, filepath.Join(dir, "printforms", "полеплохо.layout.yaml"), `name: ПолеПлохо
document: Реализация
areas:
  - name: Строка
    rows:
      - cells:
          - parameter: С
binding:
  sequence: [Строка]
  repeat:
    - area: Строка
      source: Товары
      parameters:
        С: НетПоля
`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	issues := CheckCrossRefs(proj, nil)
	var badSource, badArea, badField, orphan, anyGood bool
	for _, i := range issues {
		if strings.EqualFold(i.Object, "Хорошая") {
			anyGood = true
		}
		m := i.Message
		if strings.EqualFold(i.Object, "Плохая") {
			switch {
			case strings.Contains(m, "НетТакойТЧ"):
				badSource = true
			case strings.Contains(m, "НетТакойОбласти"):
				badArea = true
			case strings.Contains(m, "Сирота"):
				orphan = true
			}
		}
		if strings.EqualFold(i.Object, "ПолеПлохо") && strings.Contains(m, "НетПоля") {
			badField = true
		}
	}
	if !badSource {
		t.Errorf("ожидалась ошибка о несуществующей ТЧ НетТакойТЧ: %+v", issues)
	}
	if !badArea {
		t.Errorf("ожидалась ошибка о несуществующей области НетТакойОбласти: %+v", issues)
	}
	if !badField {
		t.Errorf("ожидалась ошибка о несуществующем поле НетПоля (валидная ТЧ): %+v", issues)
	}
	if !orphan {
		t.Errorf("ожидалось предупреждение о ячейке-сироте Сирота: %+v", issues)
	}
	if anyGood {
		t.Errorf("корректная форма Хорошая не должна давать ошибок: %+v", issues)
	}
}

// CheckLayoutWarnings: rowspan>1 в repeat-области даёт НЕблокирующее
// предупреждение; rowspan в обычной (одноразовой) области и repeat-область без
// rowspan — не предупреждают.
func TestCheckLayoutWarnings_RowSpanInRepeat(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "реализация.yaml"), `name: Реализация
fields:
  - name: Номер
    type: string
tableparts:
  - name: Товары
    fields:
      - name: Сумма
        type: number`)
	// repeat-область со rowspan → предупреждение.
	mkFile(t, filepath.Join(dir, "printforms", "сросспан.layout.yaml"), `name: СоСпаном
document: Реализация
areas:
  - name: Строка
    rows:
      - cells:
          - parameter: С
            rowspan: 2
binding:
  sequence: [Строка]
  repeat:
    - area: Строка
      source: Товары
      parameters:
        С: Сумма | number:2
`)
	// repeat-область без rowspan + одноразовая шапка со rowspan → без предупреждений.
	mkFile(t, filepath.Join(dir, "printforms", "безспана.layout.yaml"), `name: БезСпана
document: Реализация
areas:
  - name: Шапка
    rows:
      - cells:
          - text: "Накладная"
            rowspan: 2
  - name: Строка
    rows:
      - cells:
          - parameter: С
binding:
  sequence: [Шапка, Строка]
  repeat:
    - area: Строка
      source: Товары
      parameters:
        С: Сумма | number:2
`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	warns := CheckLayoutWarnings(proj)
	// Имя формы (Object) = имя файла без расширения; источник ТЧ — в сообщении.
	var withSpan, withoutSpan bool
	for _, w := range warns {
		if strings.Contains(w.File, "сросспан") && strings.Contains(w.Message, "rowspan") {
			withSpan = true
		}
		if strings.Contains(w.File, "безспана") {
			withoutSpan = true
		}
	}
	if !withSpan {
		t.Errorf("ожидалось предупреждение о rowspan в repeat-области (сросспан): %+v", warns)
	}
	if withoutSpan {
		t.Errorf("форма безспана (rowspan только в одноразовой шапке) не должна предупреждать: %+v", warns)
	}
}

// CheckNameCollisions должен ловить справочник и документ с одинаковым именем
// (делят одну таблицу lower(имя)) и молчать, когда имена различны (issue #20).
func TestCheckNameCollisions(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "счёт.yaml"), `name: Счёт
fields:
  - name: Наименование
    type: string`)
	mkFile(t, filepath.Join(dir, "documents", "счёт.yaml"), `name: Счёт
fields:
  - name: Номер
    type: string`)
	// Документ с уникальным именем — коллизии быть не должно.
	mkFile(t, filepath.Join(dir, "documents", "заказ.yaml"), `name: Заказ
fields:
  - name: Номер
    type: string`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	issues := CheckNameCollisions(proj)
	if len(issues) != 1 {
		t.Fatalf("ожидалась ровно одна коллизия, получено %d: %+v", len(issues), issues)
	}
	if issues[0].Object != "счёт" || !strings.Contains(issues[0].Message, "коллизия") {
		t.Errorf("неожиданная ошибка коллизии: %+v", issues[0])
	}
	if !strings.Contains(issues[0].Message, "справочник") || !strings.Contains(issues[0].Message, "документ") {
		t.Errorf("сообщение должно называть оба вида объектов: %q", issues[0].Message)
	}
}
