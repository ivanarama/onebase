package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/ivantit66/onebase/internal/dsl/langref"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/spf13/cobra"
)

var langrefCmd = &cobra.Command{
	Use:   "langref",
	Short: "Сгенерировать полный справочник встроенного языка из реестра langref",
	Long: `Печатает справочник всего встроенного языка: функции по группам, методы
объектов, конструкции языка и язык запросов — с сигнатурами, параметрами,
возвращаемыми значениями и примерами.

Источник — тот же реестр langref, что питает автодополнение в конфигураторе и
AGENTS.md, поэтому справочник не может разойтись с платформой.

Готовый результат лежит в docs/dsl-reference.md и открывается прямо на GitHub —
чтобы посмотреть язык, платформу качать не нужно.

Примеры:
  onebase langref
  onebase langref --output docs/dsl-reference.md
  onebase langref --format json`,
	RunE:          runLangref,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	langrefCmd.Flags().String("output", "", "записать справочник в файл вместо stdout")
	langrefCmd.Flags().String("format", "md", "формат вывода: md или json")
	rootCmd.AddCommand(langrefCmd)
}

func runLangref(cmd *cobra.Command, _ []string) error {
	format, _ := cmd.Flags().GetString("format")
	var text string
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "md", "markdown":
		text = renderLangrefMarkdown()
	case "json":
		b, err := json.MarshalIndent(langref.All(), "", "  ")
		if err != nil {
			return err
		}
		text = string(b) + "\n"
	default:
		return fmt.Errorf("неизвестный формат %q (ожидались md или json)", format)
	}
	out, _ := cmd.Flags().GetString("output")
	if out == "" {
		outf("%s", text)
		return nil
	}
	return os.WriteFile(out, []byte(text), fsmode.File)
}

// renderLangrefMarkdown строит полный справочник языка.
//
// Вывод обязан быть детерминированным: docs/dsl-reference.md лежит в репозитории,
// и TestDSLReferenceUpToDate сверяет файл с результатом этой функции. Поэтому
// здесь нет ни даты генерации, ни номера сборки — иначе тест краснел бы на
// каждом прогоне, а файл шумел бы в каждом диффе.
func renderLangrefMarkdown() string {
	var b strings.Builder

	byGroup := map[string][]langref.Descriptor{}
	byObject := map[string][]langref.Descriptor{}
	var keywords, queries []langref.Descriptor
	for _, d := range langref.All() {
		switch d.Kind {
		case langref.KindFunc:
			byGroup[d.Group] = append(byGroup[d.Group], d)
		case langref.KindMethod:
			byObject[d.Object] = append(byObject[d.Object], d)
		case langref.KindKeyword:
			keywords = append(keywords, d)
		case langref.KindQuery:
			queries = append(queries, d)
		}
	}
	groups, objects := langref.Groups(), langref.Objects()

	fmt.Fprintf(&b, `# Справочник встроенного языка OneBase

Полный список того, что понимает платформа: **%d функций**, **%d методов
объектов**, **%d конструкций языка** и **%d элементов языка запросов**.

Файл сгенерирован командой `+"`onebase langref`"+` из реестра `+"`internal/dsl/langref`"+` —
того же, что питает автодополнение и подсказки в конфигураторе. Разойтись с
платформой он не может: сверка живёт в тестах, и правка реестра без
перегенерации этого файла роняет сборку.

Язык русскоязычный и **регистронезависимый**: `+"`Сообщить`"+`, `+"`СООБЩИТЬ`"+` и
`+"`сообщить`"+` — одно и то же. У большинства имён есть английские синонимы, они
указаны у каждой записи.

> Как читать сигнатуру: `+"`Функция(Обязательный, [Необязательный])`"+`.
> Квадратные скобки — параметр можно не передавать.

## Содержание

- [Функции](#функции) — %d в %d группах
`, len(langrefKindSlice(langref.KindFunc)), len(langrefKindSlice(langref.KindMethod)),
		len(keywords), len(queries), len(langrefKindSlice(langref.KindFunc)), len(groups))

	for _, g := range groups {
		fmt.Fprintf(&b, "    - [%s](#%s) — %d\n", g, githubAnchor("Функции: "+g), len(byGroup[g]))
	}
	fmt.Fprintf(&b, "- [Методы объектов](#методы-объектов) — %d у %d объектов\n",
		len(langrefKindSlice(langref.KindMethod)), len(objects))
	for _, o := range objects {
		fmt.Fprintf(&b, "    - [%s](#%s) — %d\n", o, githubAnchor("Объект "+o), len(byObject[o]))
	}
	fmt.Fprintf(&b, "- [Конструкции языка](#конструкции-языка) — %d\n", len(keywords))
	fmt.Fprintf(&b, "- [Язык запросов](#язык-запросов) — %d\n", len(queries))

	b.WriteString("\n---\n\n## Функции\n")
	for _, g := range groups {
		fmt.Fprintf(&b, "\n### Функции: %s\n", g)
		writeLangrefEntries(&b, byGroup[g])
	}

	b.WriteString("\n---\n\n## Методы объектов\n")
	for _, o := range objects {
		fmt.Fprintf(&b, "\n### Объект %s\n", o)
		writeLangrefEntries(&b, byObject[o])
	}

	b.WriteString("\n---\n\n## Конструкции языка\n")
	writeLangrefEntries(&b, keywords)

	b.WriteString("\n---\n\n## Язык запросов\n")
	writeLangrefEntries(&b, queries)

	return b.String()
}

// langrefKindSlice — записи одного вида; нужен только для чисел в шапке.
func langrefKindSlice(k langref.Kind) []langref.Descriptor {
	var out []langref.Descriptor
	for _, d := range langref.All() {
		if d.Kind == k {
			out = append(out, d)
		}
	}
	return out
}

func writeLangrefEntries(b *strings.Builder, ds []langref.Descriptor) {
	sorted := make([]langref.Descriptor, len(ds))
	copy(sorted, ds)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, d := range sorted {
		writeLangrefEntry(b, d)
	}
}

func writeLangrefEntry(b *strings.Builder, d langref.Descriptor) {
	name := d.Display
	if name == "" {
		name = d.Name
	}
	fmt.Fprintf(b, "\n#### %s\n\n```\n%s\n```\n\n%s\n", name, d.Signature, d.Doc)

	if len(d.Params) > 0 {
		b.WriteString("\n| Параметр | Тип | Обяз. | Описание |\n|---|---|---|---|\n")
		for _, p := range d.Params {
			need := "да"
			if p.Optional {
				need = "нет"
			}
			fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n",
				p.Name, mdCell(p.Type), need, mdCell(p.Doc))
		}
	}
	if d.Returns != "" {
		fmt.Fprintf(b, "\n**Возвращает:** %s\n", d.Returns)
	}
	if d.Example != "" {
		fmt.Fprintf(b, "\n```\n%s\n```\n", d.Example)
	}
	if len(d.Aliases) > 0 {
		fmt.Fprintf(b, "\nСинонимы: `%s`\n", strings.Join(d.Aliases, "`, `"))
	}
}

// mdCell обезвреживает значение для ячейки таблицы: вертикальная черта режет
// таблицу на месте, перевод строки — рвёт строку таблицы целиком.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// githubAnchor повторяет правило GitHub для якорей заголовков: нижний регистр,
// пунктуация выброшена, пробелы — в дефисы. Кириллица сохраняется как есть,
// поэтому ссылки в оглавлении работают и для русских заголовков.
func githubAnchor(heading string) string {
	var out []rune
	for _, r := range strings.ToLower(heading) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-':
			out = append(out, r)
		case r == ' ':
			out = append(out, '-')
		}
	}
	return string(out)
}
