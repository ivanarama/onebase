package onec_forms

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// CompatibilityExample is executable through onebase eval --snippet --json.
// A negative example records actual OneBase behavior, not a successful port.
type CompatibilityExample struct {
	Code          string          `json:"code"`
	Result        json.RawMessage `json:"result,omitempty"`
	Messages      []string        `json:"messages,omitempty"`
	ErrorContains string          `json:"error_contains,omitempty"`
	SQLite        bool            `json:"sqlite,omitempty"`
}

// CompatibilityContract describes one signature, never a whole library API.
type CompatibilityContract struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Signature  string                 `json:"signature"`
	Verdict    string                 `json:"verdict"`
	Advice     string                 `json:"advice"`
	Reference  string                 `json:"reference"`
	RawPattern string                 `json:"raw_pattern,omitempty"`
	Method     string                 `json:"method,omitempty"`
	Examples   []CompatibilityExample `json:"examples"`
	Frequency  *ScanFrequency         `json:"frequency,omitempty"`
}

//go:embed compatibility_contracts.json
var compatibilityContractsJSON []byte

// CompatibilityMatrix keeps lexical observations separate from verdicts.
type CompatibilityMatrix struct {
	SchemaVersion      int                     `json:"schema_version"`
	Scope              string                  `json:"scope"`
	Measured           bool                    `json:"measured"`
	Contracts          []CompatibilityContract `json:"contracts"`
	UnassessedPatterns []ScanFrequency         `json:"unassessed_patterns"`
	UnassessedMethods  []ScanFrequency         `json:"unassessed_methods"`
}

// BuildCompatibilityMatrix joins the checked registry with slice 1's report.
// No match, including an unknown BSP method, implies no compatibility verdict.
func BuildCompatibilityMatrix(scan *ExportScan) (CompatibilityMatrix, error) {
	m := CompatibilityMatrix{
		SchemaVersion: 1,
		Scope:         "Проверенные сигнатуры OneBase через eval; версии 1С и БСП не сертифицированы. Контекст UI, права и полный API менеджеров требуют отдельной проверки.",
		Contracts:     []CompatibilityContract{}, UnassessedPatterns: []ScanFrequency{}, UnassessedMethods: []ScanFrequency{},
	}
	decoder := json.NewDecoder(bytes.NewReader(compatibilityContractsJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m.Contracts); err != nil {
		return m, fmt.Errorf("compatibility registry: %w", err)
	}
	if scan == nil {
		return m, nil
	}
	m.Measured = true
	for i := range m.Contracts {
		if method := m.Contracts[i].Method; method != "" {
			m.Contracts[i].Frequency = &ScanFrequency{Name: method}
		}
	}
	for _, row := range scan.RawW040 {
		matched := false
		for i := range m.Contracts {
			if m.Contracts[i].RawPattern == row.Name {
				frequency := row
				if m.Contracts[i].Method == "" {
					m.Contracts[i].Frequency = &frequency
				}
				matched = true
			}
		}
		if !matched {
			m.UnassessedPatterns = append(m.UnassessedPatterns, row)
		}
	}
	for _, row := range scan.Methods {
		matched := false
		for i := range m.Contracts {
			if strings.EqualFold(m.Contracts[i].Method, row.Name) {
				frequency := row
				m.Contracts[i].Frequency = &frequency
				matched = true
			}
		}
		if !matched {
			m.UnassessedMethods = append(m.UnassessedMethods, row)
		}
	}
	return m, nil
}

// CompatibilityMarkdown is shared by the public CLI, documentation and AI guide.
func CompatibilityMarkdown(m CompatibilityMatrix) string {
	var b strings.Builder
	b.WriteString("## Матрица переноса BSL и обращений к БСП\n\n")
	b.WriteString(m.Scope + "\n\n")
	b.WriteString("Источник — встроенный реестр проверенных контрактов плана 179, срез 2. Обновление документа: `onebase migrate-matrix > docs/bsl-compatibility.md`. JSON: `onebase migrate-matrix --json`. Частоты конкретной выгрузки: `onebase migrate-matrix /path/to/export` (те же правила и кодировки, что у `migrate-scan`).\n\n")
	b.WriteString("Вердикты: **поддерживается без изменений** — только указанная сигнатура; **механически** — указанное ручное переписывание с проверенным результатом; **нативный аналог** — реализованный и проверенный целевой контракт; **пробел платформы** — ограничение со ссылкой на заявку; **не поддерживается** — указанный исходный контракт не переносится. Команда не преобразует исходники. Наличие механизма или плана не доказывает эквивалентность API БСП.\n\n")
	b.WriteString("Примеры запускаются через `onebase eval --project examples/trade --snippet --json '<код>'`. Для помеченного SQLite-примера добавьте `--sqlite /tmp/compatibility.db`; это проверка фабрики запроса, без изменения бизнес-данных. Ошибка в отрицательном примере — ожидаемое ограничение, а не успешный перенос. Матрица не меняет диагностик W040 или результата `migrate-scan`.\n\n")
	if m.Measured {
		b.WriteString("Частоты получены текущим `migrate-scan` из входной выгрузки. Сырые W040 могут включать строки и хвостовые комментарии; частота фрагмента общая для всех его сигнатур и повторяется в их строках. Не складывайте эти строки и не считайте долю совместимости по ним. Частота метода также не проверяет его аргументы. Ноль означает измеренный ноль, «—» — отсутствие замера.\n\n")
	} else {
		b.WriteString("Выгрузка не задана: частоты **не измерены**, «—» не означает ноль. Исторические числа из плана сюда не переносятся.\n\n")
	}
	b.WriteString("| ID / конструкция | Сигнатура | Вердикт | Перенос и границы | Частота / файлов |\n|---|---|---|---|---|\n")
	cell := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", "&#124;"), "\n", " ") }
	for _, c := range m.Contracts {
		frequency := "—"
		if c.Frequency != nil {
			frequency = fmt.Sprintf("%s: %d / %d", cell(c.Frequency.Name), c.Frequency.Count, c.Frequency.Files)
		}
		fmt.Fprintf(&b, "| %s — %s | `%s` | %s | %s %s | %s |\n", c.ID, cell(c.Name), cell(c.Signature), c.Verdict, cell(c.Advice), cell(c.Reference), frequency)
	}
	b.WriteString("\n### Исполняемые примеры\n\n")
	for _, c := range m.Contracts {
		fmt.Fprintf(&b, "#### %s\n\n", c.ID)
		for _, example := range c.Examples {
			if example.SQLite {
				b.WriteString("Контекст: CLI eval с `--sqlite`.\n\n")
			}
			fmt.Fprintf(&b, "```bsl\n%s\n```\n\n", example.Code)
			if example.ErrorContains != "" {
				fmt.Fprintf(&b, "Ожидается ошибка исполнения: `%s`.\n\n", example.ErrorContains)
			} else {
				fmt.Fprintf(&b, "Результат JSON: `%s`.", example.Result)
				if len(example.Messages) > 0 {
					messages, _ := json.Marshal(example.Messages)
					fmt.Fprintf(&b, " Сообщения: `%s`.", messages)
				}
				b.WriteString("\n\n")
			}
		}
	}
	if m.Measured {
		b.WriteString("### За пределами проверенного реестра\n\nЭти наблюдения не получили вердикт; отсутствие контракта не означает ни поддержку, ни несовместимость.\n\n| Вид | Имя | Вхождений | Файлов |\n|---|---|---|---|\n")
		for _, group := range []struct {
			name string
			rows []ScanFrequency
		}{{"W040", m.UnassessedPatterns}, {"Метод БСП", m.UnassessedMethods}} {
			for _, row := range group.rows {
				fmt.Fprintf(&b, "| %s | %s | %d | %d |\n", group.name, cell(row.Name), row.Count, row.Files)
			}
		}
	}
	b.WriteString("Привязка обработки к объекту и покрытие API `ПодключаемыеКоманды` остаются пробелом по [#1479](https://github.com/ivanarama/onebase/issues/1479) и плану 135; внешний контур обработок сам по себе не является этим API. Непроверенные подсистемы, менеджеры, транзакционные окружения и редкие методы разбираются отдельно по плану 179. Эта матрица не заявляет совместимость произвольной конфигурации или библиотеки БСП.\n")
	return b.String()
}
