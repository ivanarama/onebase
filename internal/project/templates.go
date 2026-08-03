package project

import (
	"fmt"
	"github.com/ivantit66/onebase/internal/fsmode"
	"os"
	"path/filepath"
)

// ProjectTemplate describes a ready-made onebase project template.
type ProjectTemplate struct {
	Name        string
	Description string
	files       map[string]string // relative path → content
}

// templateRegistry holds all built-in templates.
var templateRegistry = map[string]*ProjectTemplate{
	"tasks":     tmplTasks,
	"crm":       tmplCRM,
	"warehouse": tmplWarehouse,
	"finance":   tmplFinance,
}

// ListTemplates returns names and descriptions of all available templates.
func ListTemplates() []ProjectTemplate {
	out := []ProjectTemplate{
		{Name: "tasks", Description: tmplTasks.Description},
		{Name: "crm", Description: tmplCRM.Description},
		{Name: "warehouse", Description: tmplWarehouse.Description},
		{Name: "finance", Description: tmplFinance.Description},
	}
	return out
}

// ApplyTemplate creates a project in dir based on the named template.
// name is used as the app name in config/app.yaml.
func ApplyTemplate(templateName, dir, appName string) error {
	tmpl, ok := templateRegistry[templateName]
	if !ok {
		return fmt.Errorf("unknown template %q; run 'onebase init --list-templates'", templateName)
	}

	// Collect unique directories
	dirs := map[string]bool{"config": true, "src": true}
	for path := range tmpl.files {
		dirs[filepath.Dir(path)] = true
	}
	for d := range dirs {
		if err := os.MkdirAll(filepath.Join(dir, d), fsmode.Dir); err != nil {
			return err
		}
	}

	for rel, content := range tmpl.files {
		dst := filepath.Join(dir, rel)
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			if err := os.WriteFile(dst, []byte(content), fsmode.File); err != nil {
				return err
			}
		}
	}

	// Write config/app.yaml with actual app name
	cfgPath := filepath.Join(dir, "config", "app.yaml")
	cfgContent := fmt.Sprintf("name: %s\nversion: \"1.0\"\n", appName)
	return os.WriteFile(cfgPath, []byte(cfgContent), fsmode.File)
}

// ── tasks ────────────────────────────────────────────────────────────────────

var tmplTasks = &ProjectTemplate{
	Name:        "tasks",
	Description: "Мини-таск-трекер: задачи, проекты, исполнители",
	files: map[string]string{
		"enums/СтатусЗадачи.yaml": `name: СтатусЗадачи
values:
  - Новая
  - В работе
  - На проверке
  - Закрыта
`,
		"enums/Приоритет.yaml": `name: Приоритет
values:
  - Низкий
  - Средний
  - Высокий
`,
		"catalogs/Проект.yaml": `name: Проект
fields:
  - name: Наименование
    type: string
  - name: Описание
    type: string
  - name: Дедлайн
    type: date
`,
		"catalogs/Исполнитель.yaml": `name: Исполнитель
fields:
  - name: Наименование
    type: string
  - name: Email
    type: string
`,
		"documents/Задача.yaml": `name: Задача
fields:
  - name: Название
    type: string
  - name: Проект
    type: reference:Проект
  - name: Исполнитель
    type: reference:Исполнитель
  - name: Дата
    type: date
  - name: Дедлайн
    type: date
  - name: Статус
    type: enum:СтатусЗадачи
  - name: Приоритет
    type: enum:Приоритет
  - name: Описание
    type: string
`,
		"reports/ЗадачиПоИсполнителям.yaml": `name: ЗадачиПоИсполнителям
title: Задачи по исполнителям
params:
  - name: Статус
    type: select
    label: Статус
    options:
      - ""
      - Новая
      - В работе
      - На проверке
      - Закрыта
  - name: Исполнитель
    type: string
    label: Исполнитель (фильтр)
query: |
  ВЫБРАТЬ
    Исполнитель,
    Статус,
    КОЛИЧЕСТВО(*) КАК КолвоЗадач
  ИЗ Документ.Задача
  ГДЕ (&Статус ЕСТЬ ПУСТО ИЛИ Статус = &Статус)
    И (&Исполнитель ЕСТЬ ПУСТО ИЛИ Исполнитель = &Исполнитель)
  СГРУППИРОВАТЬ ПО Исполнитель, Статус
  УПОРЯДОЧИТЬ ПО Исполнитель
`,
		"reports/ПросроченныеЗадачи.yaml": `name: ПросроченныеЗадачи
title: Просроченные задачи
params:
  - name: НаДату
    type: date
    label: На дату
query: |
  ВЫБРАТЬ
    Название,
    Исполнитель,
    Дедлайн,
    Статус
  ИЗ Документ.Задача
  ГДЕ Дедлайн < &НаДату
    И Статус <> 'Закрыта'
  УПОРЯДОЧИТЬ ПО Дедлайн
`,
	},
}

// ── crm ──────────────────────────────────────────────────────────────────────

var tmplCRM = &ProjectTemplate{
	Name:        "crm",
	Description: "Мини-CRM: клиенты, сделки, платежи, регистр взаиморасчётов",
	files: map[string]string{
		"enums/СтатусСделки.yaml": `name: СтатусСделки
values:
  - Новая
  - Переговоры
  - Выиграна
  - Проиграна
`,
		"catalogs/Клиент.yaml": `name: Клиент
fields:
  - name: Наименование
    type: string
  - name: Телефон
    type: string
  - name: Email
    type: string
  - name: ИНН
    type: string
  - name: ВидДеятельности
    type: reference:ВидДеятельности
`,
		"catalogs/ВидДеятельности.yaml": `name: ВидДеятельности
fields:
  - name: Наименование
    type: string
`,
		"documents/Сделка.yaml": `name: Сделка
fields:
  - name: Клиент
    type: reference:Клиент
  - name: Название
    type: string
  - name: Сумма
    type: number
  - name: Статус
    type: enum:СтатусСделки
  - name: Менеджер
    type: string
  - name: ДатаЗакрытия
    type: date
`,
		"documents/Платёж.yaml": `name: Платёж
posting: true
fields:
  - name: Клиент
    type: reference:Клиент
  - name: Сделка
    type: reference:Сделка
  - name: Сумма
    type: number
  - name: Дата
    type: date
  - name: Назначение
    type: string
`,
		"registers/Взаиморасчёты.yaml": `name: Взаиморасчёты
dimensions:
  - name: Клиент
    type: reference:Клиент
resources:
  - name: Сумма
    type: number
`,
		"src/Платёж.posting.os": `Процедура ОбработкаПроведения() Экспорт
  ДвижениеРег = Движения("Взаиморасчёты");
  ДвижениеРег.Клиент = this.Клиент;
  ДвижениеРег.Сумма = this.Сумма;
  ДвижениеРег.ВидДвижения = "Приход";
  ДвижениеРег.Записать();
КонецПроцедуры
`,
		"reports/ВоронкаПродаж.yaml": `name: ВоронкаПродаж
title: Воронка продаж
params:
  - name: Менеджер
    type: string
    label: Менеджер (фильтр)
query: |
  ВЫБРАТЬ
    Статус,
    КОЛИЧЕСТВО(*) КАК КолвоСделок,
    СУММА(Сумма) КАК СуммаСделок
  ИЗ Документ.Сделка
  ГДЕ (&Менеджер ЕСТЬ ПУСТО ИЛИ Менеджер = &Менеджер)
  СГРУППИРОВАТЬ ПО Статус
  УПОРЯДОЧИТЬ ПО СуммаСделок УБЫВ
`,
		"reports/ДолгиКлиентов.yaml": `name: ДолгиКлиентов
title: Долги клиентов
params:
  - name: НаДату
    type: date
    label: На дату
query: |
  ВЫБРАТЬ
    Клиент,
    СУММА(Сумма) КАК Долг
  ИЗ РегистрНакопления.Взаиморасчёты
  ГДЕ (&НаДату ЕСТЬ ПУСТО ИЛИ period <= &НаДату)
  СГРУППИРОВАТЬ ПО Клиент
  ИМЕЯ СУММА(Сумма) > 0
  УПОРЯДОЧИТЬ ПО Долг УБЫВ
`,
	},
}

// ── warehouse ────────────────────────────────────────────────────────────────

var tmplWarehouse = &ProjectTemplate{
	Name:        "warehouse",
	Description: "Склад: номенклатура, поступления, реализации, остатки",
	files: map[string]string{
		"catalogs/Номенклатура.yaml": `name: Номенклатура
hierarchical: true
fields:
  - name: Наименование
    type: string
  - name: Артикул
    type: string
  - name: ЕдиницаИзмерения
    type: string
  - name: ЦенаПродажи
    type: number
`,
		"catalogs/Склад.yaml": `name: Склад
fields:
  - name: Наименование
    type: string
  - name: Адрес
    type: string
`,
		"catalogs/Поставщик.yaml": `name: Поставщик
fields:
  - name: Наименование
    type: string
  - name: ИНН
    type: string
  - name: Телефон
    type: string
`,
		"documents/Поступление.yaml": `name: Поступление
posting: true
fields:
  - name: Дата
    type: date
  - name: Поставщик
    type: reference:Поставщик
  - name: Склад
    type: reference:Склад
  - name: Сумма
    type: number
tableparts:
  - name: Товары
    fields:
      - name: Номенклатура
        type: reference:Номенклатура
      - name: Количество
        type: number
      - name: Цена
        type: number
      - name: Сумма
        type: number
`,
		"documents/Реализация.yaml": `name: Реализация
posting: true
numerator:
  prefix: "РЕА-"
  length: 5
  period: year
fields:
  - name: Дата
    type: date
  - name: Покупатель
    type: string
  - name: Склад
    type: reference:Склад
  - name: Сумма
    type: number
tableparts:
  - name: Товары
    fields:
      - name: Номенклатура
        type: reference:Номенклатура
      - name: Количество
        type: number
      - name: Цена
        type: number
      - name: Сумма
        type: number
`,
		"registers/ОстаткиТоваров.yaml": `name: ОстаткиТоваров
dimensions:
  - name: Номенклатура
    type: reference:Номенклатура
  - name: Склад
    type: reference:Склад
resources:
  - name: Количество
    type: number
  - name: Сумма
    type: number
`,
		"src/Поступление.posting.os": `Процедура ОбработкаПроведения() Экспорт
  Для Каждого Стр Из this.Товары Цикл
    Дв = Движения("ОстаткиТоваров");
    Дв.Номенклатура = Стр.Номенклатура;
    Дв.Склад        = this.Склад;
    Дв.Количество   = Стр.Количество;
    Дв.Сумма        = Стр.Сумма;
    Дв.ВидДвижения  = "Приход";
    Дв.Записать();
  КонецЦикла;
КонецПроцедуры
`,
		"src/Реализация.posting.os": `Процедура ОбработкаПроведения() Экспорт
  Для Каждого Стр Из this.Товары Цикл
    Дв = Движения("ОстаткиТоваров");
    Дв.Номенклатура = Стр.Номенклатура;
    Дв.Склад        = this.Склад;
    Дв.Количество   = Стр.Количество;
    Дв.Сумма        = Стр.Сумма;
    Дв.ВидДвижения  = "Расход";
    Дв.Записать();
  КонецЦикла;
КонецПроцедуры
`,
		"reports/ОстаткиНаДату.yaml": `name: ОстаткиНаДату
title: Остатки товаров на дату
params:
  - name: НаДату
    type: date
    label: На дату
  - name: Склад
    type: string
    label: Склад (фильтр)
query: |
  ВЫБРАТЬ
    Номенклатура,
    Склад,
    СУММА(Количество) КАК Количество,
    СУММА(Сумма)      КАК Сумма
  ИЗ РегистрНакопления.ОстаткиТоваров
  ГДЕ (&НаДату ЕСТЬ ПУСТО ИЛИ period <= &НаДату)
    И (&Склад ЕСТЬ ПУСТО ИЛИ Склад = &Склад)
  СГРУППИРОВАТЬ ПО Номенклатура, Склад
  ИМЕЯ СУММА(Количество) > 0
  УПОРЯДОЧИТЬ ПО Номенклатура
`,
	},
}

// ── finance ──────────────────────────────────────────────────────────────────

var tmplFinance = &ProjectTemplate{
	Name:        "finance",
	Description: "Домашние финансы: счета, категории, операции, остатки",
	files: map[string]string{
		"enums/ВидОперации.yaml": `name: ВидОперации
values:
  - Доход
  - Расход
  - Перевод
`,
		"catalogs/СчётДС.yaml": `name: СчётДС
fields:
  - name: Наименование
    type: string
  - name: Валюта
    type: string
  - name: НачальныйОстаток
    type: number
`,
		"catalogs/Категория.yaml": `name: Категория
hierarchical: true
fields:
  - name: Наименование
    type: string
  - name: ВидОперации
    type: enum:ВидОперации
`,
		"documents/Операция.yaml": `name: Операция
posting: true
fields:
  - name: Дата
    type: date
  - name: Счёт
    type: reference:СчётДС
  - name: Категория
    type: reference:Категория
  - name: Сумма
    type: number
  - name: ВидОперации
    type: enum:ВидОперации
  - name: Комментарий
    type: string
`,
		"registers/ДенежныеСредства.yaml": `name: ДенежныеСредства
dimensions:
  - name: Счёт
    type: reference:СчётДС
  - name: Категория
    type: reference:Категория
resources:
  - name: Сумма
    type: number
`,
		"src/Операция.posting.os": `Процедура ОбработкаПроведения() Экспорт
  Дв = Движения("ДенежныеСредства");
  Дв.Счёт      = this.Счёт;
  Дв.Категория = this.Категория;
  Дв.Сумма     = this.Сумма;
  Если this.ВидОперации = "Расход" Тогда
    Дв.ВидДвижения = "Расход";
  Иначе
    Дв.ВидДвижения = "Приход";
  КонецЕсли;
  Дв.Записать();
КонецПроцедуры
`,
		"reports/ОстаткиПоСчетам.yaml": `name: ОстаткиПоСчетам
title: Остатки по счетам
params:
  - name: НаДату
    type: date
    label: На дату
query: |
  ВЫБРАТЬ
    Счёт,
    СУММА(Сумма) КАК Остаток
  ИЗ РегистрНакопления.ДенежныеСредства
  ГДЕ (&НаДату ЕСТЬ ПУСТО ИЛИ period <= &НаДату)
  СГРУППИРОВАТЬ ПО Счёт
  УПОРЯДОЧИТЬ ПО Счёт
`,
		"reports/РасходыПоКатегориям.yaml": `name: РасходыПоКатегориям
title: Расходы по категориям
params:
  - name: Начало
    type: date
    label: "С даты"
  - name: Конец
    type: date
    label: "По дату"
query: |
  ВЫБРАТЬ
    Категория,
    СУММА(Сумма) КАК Сумма
  ИЗ РегистрНакопления.ДенежныеСредства
  ГДЕ ВидДвижения = 'Расход'
    И (&Начало ЕСТЬ ПУСТО ИЛИ period >= &Начало)
    И (&Конец ЕСТЬ ПУСТО ИЛИ period <= &Конец)
  СГРУППИРОВАТЬ ПО Категория
  УПОРЯДОЧИТЬ ПО Сумма УБЫВ
`,
	},
}
