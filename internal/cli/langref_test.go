package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/langref"
)

// dslReferencePath — путь до витрины от каталога пакета.
const dslReferencePath = "../../docs/dsl-reference.md"

// TestDSLReferenceUpToDate — гейт против ровно того сценария, ради которого
// витрина и заводилась: справочник, который тихо отстал от платформы, хуже
// отсутствующего — он выглядит достоверным. Добавили функцию в реестр и не
// перегенерировали файл — сборка красная.
func TestDSLReferenceUpToDate(t *testing.T) {
	want := renderLangrefMarkdown()
	raw, err := os.ReadFile(dslReferencePath)
	if err != nil {
		t.Fatalf("прочитать %s: %v", dslReferencePath, err)
	}
	// Файл хранится в LF (.gitattributes), но на Windows рабочая копия может
	// быть в CRLF — сравнивать надо содержимое, а не перевод строки.
	got := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if got == want {
		return
	}
	t.Errorf("docs/dsl-reference.md разошёлся с реестром langref.\n"+
		"Перегенерируйте: onebase langref --output docs/dsl-reference.md\n"+
		"(в файле %d строк, ожидалось %d)", strings.Count(got, "\n"), strings.Count(want, "\n"))
	for i, line := range strings.Split(want, "\n") {
		gotLines := strings.Split(got, "\n")
		if i >= len(gotLines) {
			t.Errorf("первое расхождение: строка %d, в файле её нет; ожидалась %q", i+1, line)
			break
		}
		if gotLines[i] != line {
			t.Errorf("первое расхождение в строке %d:\n  в файле:   %q\n  ожидалось: %q", i+1, gotLines[i], line)
			break
		}
	}
}

// TestDSLReferenceCoversEveryDescriptor проверяет не «файл совпадает с
// генератором», а «в файле есть каждая запись реестра». Без этого достаточно
// сломать группировку в самом генераторе — и сверка выше останется зелёной,
// потому что сравнивает вывод с самим собой.
func TestDSLReferenceCoversEveryDescriptor(t *testing.T) {
	raw, err := os.ReadFile(dslReferencePath)
	if err != nil {
		t.Fatalf("прочитать %s: %v", dslReferencePath, err)
	}
	text := string(raw)
	all := langref.All()
	if len(all) == 0 {
		t.Fatal("реестр langref пуст — проверять нечего")
	}
	missing := 0
	for _, d := range all {
		name := d.Display
		if name == "" {
			name = d.Name
		}
		if !strings.Contains(text, "\n#### "+name+"\n") {
			if missing < 10 {
				t.Errorf("в справочнике нет записи %q (%s)", name, d.Kind)
			}
			missing++
		}
	}
	if missing > 10 {
		t.Errorf("...и ещё %d записей", missing-10)
	}
}

// TestLangrefMarkdownDeterministic — вывод обязан быть побайтово одинаковым от
// прогона к прогону, иначе сверка выше начнёт краснеть случайным образом:
// дескрипторы группируются через map, а порядок обхода map в Go намеренно
// рандомизирован.
func TestLangrefMarkdownDeterministic(t *testing.T) {
	first := renderLangrefMarkdown()
	for i := 0; i < 20; i++ {
		if got := renderLangrefMarkdown(); got != first {
			t.Fatalf("прогон %d дал другой результат — вывод недетерминирован", i+2)
		}
	}
}

// TestLangrefTableCellsEscaped — значения из реестра попадают в ячейки таблицы
// markdown, где вертикальная черта режет таблицу, а перевод строки рвёт строку.
// Проверяем на самом реестре: появится описание с «|», и таблица развалится
// молча — в отрендеренном виде это просто лишняя колонка.
func TestLangrefTableCellsEscaped(t *testing.T) {
	for _, d := range langref.All() {
		for _, p := range d.Params {
			for _, v := range []string{mdCell(p.Type), mdCell(p.Doc)} {
				if strings.Contains(v, "\n") || strings.Contains(v, "\r") {
					t.Errorf("%s: перевод строки в ячейке параметра %q", d.Name, p.Name)
				}
				if strings.Contains(strings.ReplaceAll(v, "\\|", ""), "|") {
					t.Errorf("%s: неэкранированная «|» в ячейке параметра %q: %q", d.Name, p.Name, v)
				}
			}
		}
	}
}

func TestGithubAnchor(t *testing.T) {
	cases := map[string]string{
		"Функции: Строки":           "функции-строки",
		"Функции: HTTP-сервисы":     "функции-http-сервисы",
		"Объект Диаграмма.Серии":    "объект-диаграммасерии",
		"Функции: Коллекции и JSON": "функции-коллекции-и-json",
	}
	for in, want := range cases {
		if got := githubAnchor(in); got != want {
			t.Errorf("githubAnchor(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

// TestLangrefTOCAnchorsResolve — оглавление ссылается на заголовки того же
// файла. Ссылка, ведущая в никуда, в markdown не подсвечивается: она просто
// ничего не делает при клике, и заметить это можно только руками.
func TestLangrefTOCAnchorsResolve(t *testing.T) {
	text := renderLangrefMarkdown()
	headings := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		if h := strings.TrimLeft(line, "#"); h != line {
			headings[githubAnchor(strings.TrimSpace(h))] = true
		}
	}
	checked := 0
	for _, line := range strings.Split(text, "\n") {
		i := strings.Index(line, "](#")
		if i < 0 {
			continue
		}
		anchor := line[i+3:]
		if j := strings.Index(anchor, ")"); j >= 0 {
			anchor = anchor[:j]
		}
		if !headings[anchor] {
			t.Errorf("оглавление ссылается на #%s — такого заголовка в файле нет", anchor)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("в оглавлении не нашлось ни одной ссылки — тест ничего не проверил")
	}
	if _, err := os.Stat(filepath.Clean(dslReferencePath)); err != nil {
		t.Fatalf("витрина отсутствует: %v", err)
	}
}
