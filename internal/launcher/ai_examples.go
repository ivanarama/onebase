package launcher

import (
	"sort"
	"strings"
)

// Эталонные примеры объектов для инструмента «пример_объекта». Полные,
// проходящие check образцы, сверенные с examples/ — модель копирует формат
// реального объекта вместо угадывания. Дополняют компактные памятки в промпте
// (те всегда под рукой; эти — глубокий пример по запросу для нужного типа).

const exCatalog = `name: Номенклатура
title: Номенклатура
hierarchical: true
fields:
  - {name: Наименование, type: string}
  - {name: Артикул, type: string}
  - {name: ЕдиницаИзмерения, type: reference:ЕдиницаИзмерения}
  - {name: Цена, type: number}
`

const exDocument = `name: РеализацияТоваров
title: Реализация товаров
posting: true
numerator: {prefix: "РТ-", length: 6, period: year}
fields:
  - {name: Дата, type: date}
  - {name: Контрагент, type: reference:Контрагент}
  - {name: Склад, type: reference:Склад}
tableparts:
  - name: Товары
    fields:
      - {name: Номенклатура, type: reference:Номенклатура}
      - {name: Количество, type: number}
      - {name: Цена, type: number}
      - {name: Сумма, type: number}
# Проведение — отдельный файл src/РеализацияТоваров.posting.os (тип «проведение»).
`

const exRegister = `name: ОстаткиТоваров
dimensions:
  - {name: Номенклатура, type: reference:Номенклатура}
  - {name: Склад, type: reference:Склад}
resources:
  - {name: Количество, type: number}
  - {name: Сумма, type: number}
# Ключа type у регистра накопления нет.
`

const exInfoReg = `name: ЦеныНоменклатуры
periodic: false
dimensions:
  - {name: Номенклатура, type: reference:Номенклатура}
  - {name: ТипЦены, type: string}
resources:
  - {name: Цена, type: number}
`

const exEnum = `name: СтатусЗаказа
values:
  - Новый
  - ВРаботе
  - Выполнен
`

const exReport = `name: ОстаткиТоваров
title: Остатки товаров на дату
params:
  - {name: НаДату, type: date, label: На дату}
query: |
  ВЫБРАТЬ Номенклатура, СУММА(Количество) КАК Количество
  ИЗ РегистрНакопления.ОстаткиТоваров
  ГДЕ (&НаДату ЕСТЬ ПУСТО ИЛИ period <= &НаДату)
  СГРУППИРОВАТЬ ПО Номенклатура
  ИМЕЯ СУММА(Количество) > 0
`

const exWidget = `name: ВыручкаМесяца
type: kpi
title: Выручка за месяц
format: money
query: |
  ВЫБРАТЬ СУММА(Сумма) КАК Значение
  ИЗ РегистрНакопления.Взаиморасчеты
  ГДЕ вид_движения = 'Приход'
`

const exChartOfAccounts = `name: Основной
title: Основной план счетов
accounts:
  - {code: "01", name: Основные средства, kind: active}
  - {code: "51", name: Расчётный счёт, kind: active}
  - {code: "60", name: Расчёты с поставщиками, kind: active_passive}
  - {code: "90", name: Продажи, kind: active_passive}
  - {code: "90.1", name: Выручка, kind: passive, parent: "90"}
`

const exAccountReg = `name: БухУчёт
title: Бухгалтерский учёт
accounts: Основной
resources:
  - {name: Сумма, type: number}
subconto:
  - {name: Контрагент, type: reference:Контрагент}
  - {name: Номенклатура, type: reference:Номенклатура}
`

const exRole = `name: Менеджер
description: Работа с продажами
permissions:
  catalogs:
    Номенклатура: [read, write]
    Контрагент: [read, write]
  documents:
    РеализацияТоваров: [read, write, post, unpost]
  registers:
    ОстаткиТоваров: [read]
  reports:
    ОстаткиТоваров: [run]
`

const exService = `name: API
title: Публичное API
root_url: api
auth: none
templates:
  - template: /health
    methods: {GET: Здоровье}
  - template: /номенклатура/{артикул}
    methods: {GET: НоменклатураПоАртикулу}
# Обработчики процедур — в src/API.service.os.
`

const exScheduled = `name: ПересчётЦен
title: Пересчёт цен (ежедневно в 3:00)
schedule: "0 3 * * *"
processor: ПересчётЦен
params: {Процент: 5}
enabled: true
on_error: continue
timeout: 60
`

const exPage = `name: ПанельРуководителя
title: Панель руководителя
icon: layout-dashboard
# Обработчик — src/ПанельРуководителя.page.os:
#   Процедура ПриФормировании(Страница, Параметры) Экспорт
#       Страница.Заголовок("Сводка");
#       Страница.Показатель("Выручка за месяц", 100000, "money");
#   КонецПроцедуры
`

const exJournal = `name: ЖурналПродаж
title: Журнал продаж
documents: [РеализацияТоваров, ЗаказПокупателя]
columns:
  - {field: Дата, label: Дата, format: date}
  - {field: Контрагент, label: Контрагент}
  - {field: Сумма, label: Сумма}
filters:
  - {field: Дата, label: Дата, type: date_range}
  - {field: Контрагент, label: Контрагент, type: reference:Контрагент}
`

const exProcessor = `name: ПересчётЦен
title: Пересчёт цен
# Код — отдельный файл src/пересчётцен.proc.os (нижний регистр, пробелы → «_»):
#   Процедура Выполнить()
#       Сообщить("Пересчёт цен выполнен");
#   КонецПроцедуры
`

const exConstants = `constants:
  - {name: ОсновнойСклад, type: reference:Склад, label: Основной склад}
  - {name: КонтролироватьОстатки, type: bool, default: true, label: Контролировать остатки}
  - {name: НазваниеОрганизации, type: string, label: Название организации}
`

const exPrintform = `# printforms/счётнаоплату.layout.yaml — имя формы берётся из имени файла
name: СчётНаОплату
document: РеализацияТоваров
columns:
  - {width: 40px}
  - {}
  - {}
  - {}
areas:
  - name: Заголовок
    rows:
      - cells:
          - {text: "Счёт № {{Номер}} от {{Дата | date}}", bold: true, align: center, colspan: 4}
  - name: Шапка
    rows:
      - cells:
          - {text: "Покупатель: {{Контрагент.Наименование}}", colspan: 4}
  - name: ШапкаТаблицы
    rows:
      - cells:
          - {text: "№", bold: true, border: all}
          - {text: Наименование, bold: true, border: all}
          - {text: Кол-во, bold: true, align: right, border: all}
          - {text: Сумма, bold: true, align: right, border: all}
  - name: Строка
    rows:
      - cells:
          - {parameter: Кол0, border: all}
          - {parameter: Кол1, border: all}
          - {parameter: Кол2, align: right, border: all}
          - {parameter: Кол3, align: right, border: all}
  - name: Итоги
    rows:
      - cells:
          - {}
          - {}
          - {}
          - {text: "Итого: {{Итог.Товары.Сумма | number:2}}", bold: true, align: right, border: all}
binding:
  sequence: [Заголовок, Шапка, ШапкаТаблицы, Строка, Итоги]
  repeat:
    - area: Строка
      source: Товары
      parameters:
        Кол0: "@row"
        Кол1: Номенклатура.Наименование
        Кол2: Количество
        Кол3: Сумма | number:2
`

const exSubsystem = `name: Продажи
title: Продажи
icon: shopping-cart
order: 10
contents:
  documents: [РеализацияТоваров, ЗаказПокупателя]
  catalogs: [Номенклатура, Контрагент]
  journals: [ЖурналПродаж]
  reports: [ОстаткиТоваров]
home_page:
  layout: rows
  rows:
    - {widgets: [ВыручкаМесяца]}
`

const exForm = `# forms/реализациятоваров/ФормаОбъекта.form.yaml
schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: РеализацияТоваров
  title: {ru: "Реализация товаров"}
attributes:
  - {name: Объект, type: DocumentRef.РеализацияТоваров, main: true, save: true}
elements:
  - kind: ГруппаФормы
    name: Шапка
    children:
      - {kind: ПолеВвода, name: ПолеДата, data_path: Объект.Дата}
      - {kind: ПолеВвода, name: ПолеКонтрагент, data_path: Объект.Контрагент}
  - {kind: ТабличнаяЧасть, name: ТабТовары, data_path: Объект.Товары}
events: {ПриОткрытии: ПриОткрытииФормы}
# У документа/справочника форма уже автогенерируется — пиши .form.yaml только под кастомный макет.
`

const exPosting = `// src/РеализацияТоваров.posting.os
Процедура ОбработкаПроведения()
    Движения.ОстаткиТоваров.Очистить();
    Для Каждого Стр Из this.Товары Цикл
        Дв = Движения.ОстаткиТоваров.Добавить();
        Дв.ВидДвижения  = "Расход";
        Дв.Номенклатура = Стр.Номенклатура;
        Дв.Склад        = this.Склад;
        Дв.Количество   = Стр.Количество;
        Дв.Сумма        = Стр.Сумма;
    КонецЦикла;
КонецПроцедуры
`

// exampleForType возвращает эталонный пример для типа объекта (по синонимам,
// регистронезависимо), совпадающим с kindSubdir + «форма»/«проведение».
func exampleForType(kind string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "справочник", "каталог", "catalog":
		return exCatalog, true
	case "документ", "document":
		return exDocument, true
	case "регистр накопления", "регистрнакопления", "регистр", "register":
		return exRegister, true
	case "регистр сведений", "регистрсведений", "inforegister":
		return exInfoReg, true
	case "перечисление", "enum":
		return exEnum, true
	case "отчёт", "отчет", "report":
		return exReport, true
	case "виджет", "widget":
		return exWidget, true
	case "план счетов", "плансчетов", "chartofaccounts":
		return exChartOfAccounts, true
	case "регистр бухгалтерии", "регистрбухгалтерии", "accountregister":
		return exAccountReg, true
	case "роль", "role":
		return exRole, true
	case "http-сервис", "http service", "сервис", "service":
		return exService, true
	case "регламентное задание", "задание", "scheduled", "job":
		return exScheduled, true
	case "страница", "page":
		return exPage, true
	case "журнал", "журнал документов", "journal":
		return exJournal, true
	case "подсистема", "subsystem":
		return exSubsystem, true
	case "обработка", "processor":
		return exProcessor, true
	case "константы", "константа", "constants":
		return exConstants, true
	case "печатная форма", "печатнаяформа", "printform":
		return exPrintform, true
	case "форма", "управляемая форма", "form":
		return exForm, true
	case "проведение", "обработкапроведения", "posting":
		return exPosting, true
	}
	return "", false
}

// exampleTypeNames — отсортированный список поддерживаемых типов (для подсказки,
// когда запрошен неизвестный тип).
func exampleTypeNames() []string {
	names := []string{
		"справочник", "документ", "регистр", "регистр сведений", "перечисление",
		"отчёт", "виджет", "план счетов", "регистр бухгалтерии", "роль", "сервис",
		"задание", "страница", "журнал", "подсистема", "форма", "проведение",
		"обработка", "константы", "печатная форма",
	}
	sort.Strings(names)
	return names
}
