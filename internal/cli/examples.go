package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var examplesCmd = &cobra.Command{
	Use:   "examples [kind]",
	Short: "Показать канонический мини-пример объекта конфигурации",
	Long: `Печатает короткий валидный пример YAML или DSL для выбранного вида объекта.
Команда полезна для ИИ-ассистентов и разработчиков: вместо угадывания структуры
можно запросить эталонный фрагмент и адаптировать его под текущую базу.

Примеры:
  onebase examples catalog
  onebase examples document
  onebase examples query`,
	Args:          cobra.MaximumNArgs(1),
	RunE:          runExamples,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	examplesCmd.Flags().Bool("list", false, "показать доступные виды примеров")
	rootCmd.AddCommand(examplesCmd)
}

func runExamples(cmd *cobra.Command, args []string) error {
	list, _ := cmd.Flags().GetBool("list")
	if list || len(args) == 0 {
		outln(strings.Join(exampleKinds(), "\n"))
		return nil
	}
	text, ok := exampleSnippet(args[0])
	if !ok {
		return fmt.Errorf("неизвестный вид примера %q\nдоступно:\n%s", args[0], strings.Join(exampleKinds(), "\n"))
	}
	outf("%s", text)
	if !strings.HasSuffix(text, "\n") {
		outln()
	}
	return nil
}

func exampleKinds() []string {
	out := make([]string, 0, len(exampleSnippets))
	for k := range exampleSnippets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func exampleSnippet(kind string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(kind))
	key = strings.ReplaceAll(key, "_", "-")
	if alias, ok := exampleAliases[key]; ok {
		key = alias
	}
	v, ok := exampleSnippets[key]
	return v, ok
}

var exampleAliases = map[string]string{
	"catalog":      "catalog",
	"справочник":   "catalog",
	"document":     "document",
	"документ":     "document",
	"register":     "register",
	"регистр":      "register",
	"inforeg":      "inforeg",
	"enum":         "enum",
	"перечисление": "enum",
	"processor":    "processor",
	"обработка":    "processor",
	"report":       "report",
	"отчёт":        "report",
	"отчет":        "report",
	"widget":       "widget",
	"виджет":       "widget",
	"form":         "form",
	"форма":        "form",
	"page":         "page",
	"страница":     "page",
	"service":      "service",
	"сервис":       "service",
	"role":         "role",
	"роль":         "role",
	"query":        "query",
	"запрос":       "query",
	"posting":      "posting",
	"проведение":   "posting",
}

var exampleSnippets = map[string]string{
	"catalog": `name: Контрагент
title: Контрагенты
fields:
  - {name: Наименование, type: string}
  - {name: ИНН, type: string}
  - {name: Активен, type: bool}
`,
	"document": `name: ЗаказПокупателя
title: Заказ покупателя
posting: true
numerator: {prefix: "ЗП-", length: 6, period: year}
fields:
  - {name: Дата, type: date}
  - {name: Контрагент, type: reference:Контрагент}
tableparts:
  - name: Товары
    fields:
      - {name: Номенклатура, type: reference:Номенклатура}
      - {name: Количество, type: number}
      - {name: Цена, type: number}
      - {name: Сумма, type: number}
`,
	"register": `name: ОстаткиТоваров
title: Остатки товаров
dimensions:
  - {name: Номенклатура, type: reference:Номенклатура}
  - {name: Склад, type: reference:Склад}
resources:
  - {name: Количество, type: number}
attributes:
  - {name: ДокументОснование, type: string}
`,
	"inforeg": `name: ЦеныНоменклатуры
title: Цены номенклатуры
periodic: true
dimensions:
  - {name: Номенклатура, type: reference:Номенклатура}
resources:
  - {name: Цена, type: number}
`,
	"enum": `name: СтатусЗаказа
values:
  - Новый
  - ВРаботе
  - Завершён
  - Отменён
`,
	"processor": `name: ПересчитатьЦены
title: Пересчитать цены
params:
  - {name: Коэффициент, type: number, label: Коэффициент}
  - {name: ТолькоАктивные, type: bool, label: Только активные}
`,
	"report": `name: ПродажиПоКонтрагентам
title: Продажи по контрагентам
params:
  - {name: Начало, type: date, label: Начало периода}
  - {name: Конец, type: date, label: Конец периода}
query: |
  ВЫБРАТЬ
    Контрагент,
    СУММА(Сумма) КАК Сумма
  ИЗ Документ.ЗаказПокупателя
  ГДЕ Дата МЕЖДУ &Начало И &Конец
  СГРУППИРОВАТЬ ПО Контрагент
composition:
  groupings: [Контрагент]
  measures:
    - {field: Сумма, agg: sum, title: Сумма, format: "#,##0.00"}
`,
	"widget": `name: ПродажиМесяца
type: chart
title: Продажи месяца
query: |
  ВЫБРАТЬ
    НачалоМесяца(Дата) КАК Период,
    СУММА(Сумма) КАК Сумма
  ИЗ Документ.ЗаказПокупателя
  СГРУППИРОВАТЬ ПО НачалоМесяца(Дата)
chart_kind: line
x_field: Период
y_fields: [Сумма]
`,
	"form": `entity: ЗаказПокупателя
name: ФормаОбъекта
kind: object
layout_kind: managed
title:
  ru: Заказ покупателя
events:
  ПриОткрытии: ПриОткрытии
elements:
  - name: Дата
    kind: ПолеДаты
    field: Дата
  - name: Контрагент
    kind: ПолеВвода
    field: Контрагент
  - name: Товары
    kind: ТабличнаяЧасть
    table_part: Товары
`,
	"page": `name: ПанельПродаж
title: Панель продаж
icon: bar-chart-3
roles: [Менеджер]
params: [Период]
`,
	"service": `name: api
title: API интеграции
root_url: api
auth: token
secret: "${env:ONEBASE_API_TOKEN}"
rate_limit: 120
templates:
  - template: /orders/{id}
    methods:
      GET: ПолучитьЗаказ
      POST: ОбновитьЗаказ
`,
	"role": `name: Менеджер
description: Продажи и базовые справочники
permissions:
  catalogs:
    Контрагент: [read, write]
  documents:
    ЗаказПокупателя: [read, write, post, unpost]
  reports:
    ПродажиПоКонтрагентам: [run]
  processors:
    ПересчитатьЦены: [run]
`,
	"query": `ВЫБРАТЬ
  Номенклатура,
  СУММА(КоличествоОстаток) КАК Остаток
ИЗ РегистрНакопления.ОстаткиТоваров.Остатки(&НаДату)
СГРУППИРОВАТЬ ПО Номенклатура
УПОРЯДОЧИТЬ ПО Номенклатура
`,
	"posting": `Процедура Проведение() Экспорт
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Движение = Движения.ОстаткиТоваров.Добавить();
    Движение.ВидДвижения = "Расход";
    Движение.Номенклатура = Стр.Номенклатура;
    Движение.Склад = ЭтотОбъект.Склад;
    Движение.Количество = Стр.Количество;
  КонецЦикла;
КонецПроцедуры
`,
}
