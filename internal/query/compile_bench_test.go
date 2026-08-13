package query_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/query"
)

// Компиляция идёт на КАЖДОМ Запрос.Выполнить(), кэша нет. Бенчмарк меряет её на
// двух размерах: короткий запрос — нижняя граница накладных расходов, длинный —
// реальный отчёт с 30 агрегатами и группировкой.
//
// Текст намеренно кириллический: у strings.ToUpper есть ASCII-fast-path, и на
// латинице стоимость апперкейса совсем другая — латинский бенчмарк показал бы
// картину, которой в кириллических конфигурациях не бывает.

const benchShortQuery = `ВЫБРАТЬ
  Номенклатура,
  СУММА(Количество) КАК Количество
ИЗ РегистрНакопления.ТоварноеДвижение
ГДЕ Номенклатура = &Номенклатура
СГРУППИРОВАТЬ ПО Номенклатура
УПОРЯДОЧИТЬ ПО Номенклатура`

// benchLongQuery собирается из тех же кирпичей, что и реальный отчёт по
// статистике обменов: 30 суммируемых ресурсов, семь измерений в группировке.
func benchLongQuery() string {
	resources := []string{
		"КоличествоЦиклов", "КоличествоОшибок", "КоличествоОбъектов", "ОбъемДанных",
		"Длительность", "ОпросОтправителя", "ОпросПолучателя", "ДействияПередВыгрузкой",
		"ДействияПередВыгрузкойНаУзле", "ПолучениеСоставаОчереди", "ОбменПоТипам",
		"ОпределениеПравила", "ПолучениеСостава", "ПередЗапускомПотока",
		"ПередЗапускомПотокаНаУзле", "ПолучениеИзменений", "Распаковка", "Подготовка",
		"ДействияПередЗагрузкой", "ОтправкаИзменений", "ОчисткаОчереди",
		"ЛогиИСтатистика", "Прочее",
	}
	dimensions := []string{"День", "Час", "ПланОбмена", "Отправитель", "Получатель", "ИмяПотока", "ИмяТипа"}

	var sb strings.Builder
	sb.WriteString("ВЫБРАТЬ\n")
	for _, d := range dimensions {
		sb.WriteString("\tСтатистика." + d + " КАК " + d + ",\n")
	}
	for i, r := range resources {
		sb.WriteString("\tСУММА(Статистика." + r + ") КАК " + r)
		if i < len(resources)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("ИЗ РегистрСведений.СтатистикаОбменов КАК Статистика\n")
	sb.WriteString("ГДЕ Статистика.Час < &Час\n")
	sb.WriteString("СГРУППИРОВАТЬ ПО\n")
	for i, d := range dimensions {
		sb.WriteString("\tСтатистика." + d)
		if i < len(dimensions)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("УПОРЯДОЧИТЬ ПО День, Час")
	return sb.String()
}

func benchmarkCompile(b *testing.B, src string) {
	b.Helper()
	if _, err := query.Compile(src, query.CompileOpts{}); err != nil {
		b.Fatalf("проба не компилируется: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := query.Compile(src, query.CompileOpts{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileShort(b *testing.B) { benchmarkCompile(b, benchShortQuery) }

func BenchmarkCompileLong(b *testing.B) { benchmarkCompile(b, benchLongQuery()) }
