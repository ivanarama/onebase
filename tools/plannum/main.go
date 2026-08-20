// plannum — гейт уникальности номеров планов в Plans/.
//
// Зачем. Номер плана — это ссылка: в коде живут комментарии «план 74», в
// коммитах «feat(план 46)», в обсуждениях «см. план 52». Пока номер занят одной
// темой, ссылку можно проверить. Когда под номером оказываются разные планы,
// проверить её нельзя вовсе: «план 74» означал и свёртку базы, и шину real-time
// (#1035), «план 52» — и потокобезопасность интерпретатора, и синтакс-помощник.
// Развести это потом стоит куда дороже, чем не допустить: пятнадцать переездов
// в #1035 потребовали править ссылки в коде, в планах и в README.
//
// Что проверяется:
//
//  1. Номер занят одним планом. Несколько файлов под номером — норма, если это
//     части ОДНОГО плана (`-impl`, `-design`, `-stageN`, `-demo`); такие номера
//     перечислены в multiPart с указанием темы.
//  2. Заголовок плана называет свой номер. Иначе номер живёт только в имени
//     файла, и следующий автор занимает его, не заметив (так появились
//     коллизии 06, 31 и 32).
//  3. В multiPart нет протухших записей: исключение, переставшее быть правдой,
//     хуже отсутствующего — оно молча разрешает новую коллизию.
//
// Запуск:
//
//	go run ./tools/plannum            # проверка, ненулевой код при нарушении
//	go run ./tools/plannum -dir Plans # другой каталог
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// multiPart — номера, под которыми лежат части одного плана. Значение — тема,
// чтобы запись нельзя было оставить без объяснения.
var multiPart = map[int]string{
	36:  "дорожная карта DSL: обзор + детализация",
	50:  "пометка удаления и проведение: план + реализация",
	51:  "ИИ-помощник для бизнеса: план + follow-up'ы + реализация",
	55:  "раскол монолита и embed фронтенда: план + три итерации реализации",
	57:  "ИИ-конфигуратор: этапы 0–3, у каждого спека и реализация",
	59:  "компоновка отчётов: план, дизайн и две реализации",
	63:  "фиксы ишью #48/#49: дизайн-спека + план реализации",
	86:  "обмен данными: план + сценарий демонстрации",
	146: "синтакс-помощник: план + реализация",
	149: "визуальный конструктор форм: план + доводка",
}

var (
	fileNum = regexp.MustCompile(`^(\d+)-`)
	// «План 74», «Этапа 6», «plan 45», а также «# 108. …» и «# 72 — …»:
	// номер, названный в заголовке в любой из принятых в репозитории форм.
	headerNum = regexp.MustCompile(`(?i:план|этап|plan|stage)[а-яё]*\s+(\d+)|^#\s+(\d+)[.\s—-]`)
)

func main() {
	dir := flag.String("dir", "Plans", "каталог планов")
	flag.Parse()

	problems, total, err := check(*dir, multiPart)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plannum:", err)
		os.Exit(2)
	}
	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "plannum: номера планов не уникальны")
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "  -", p)
		}
		os.Exit(1)
	}
	fmt.Printf("plannum: %d планов, номера уникальны\n", total)
}

// check возвращает список нарушений и количество занятых номеров. Список
// отсортирован, чтобы вывод не плясал от порядка обхода каталога.
func check(dir string, allowed map[int]string) ([]string, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}

	byNum := map[int][]string{}
	var problems []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		m := fileNum.FindStringSubmatch(name)
		if m == nil {
			continue // README.md и прочие ненумерованные — не планы
		}
		num, _ := strconv.Atoi(m[1])
		byNum[num] = append(byNum[num], name)

		head, err := firstHeading(filepath.Join(dir, name))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if !headingClaims(head, num) {
			problems = append(problems, fmt.Sprintf(
				"%s: заголовок не называет номер %d (%q) — номер живёт только в имени файла, "+
					"и следующий автор займёт его, не заметив", name, num, head))
		}
	}

	nums := make([]int, 0, len(byNum))
	for n := range byNum {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	for _, n := range nums {
		files := byNum[n]
		if len(files) < 2 {
			continue
		}
		sort.Strings(files)
		if _, ok := allowed[n]; !ok {
			problems = append(problems, fmt.Sprintf(
				"номер %d делят разные планы: %s — переименуйте более поздний на свободный номер "+
					"(если это части одного плана, добавьте номер в multiPart с описанием темы)",
				n, strings.Join(files, ", ")))
		}
	}
	for n, topic := range allowed {
		if len(byNum[n]) < 2 {
			problems = append(problems, fmt.Sprintf(
				"multiPart[%d] («%s») протух: под номером %d файлов %d — уберите запись, "+
					"иначе исключение молча разрешит новую коллизию", n, topic, n, len(byNum[n])))
		}
	}
	sort.Strings(problems)
	return problems, len(byNum), nil
}

// firstHeading возвращает первую строку-заголовок файла.
func firstHeading(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return line, nil
		}
	}
	return "", fmt.Errorf("нет заголовка")
}

// headingClaims — называет ли заголовок номер плана. Ищем все числа, названные
// как номер плана: «План 74», «Этап 6 —», «# 72 — …».
func headingClaims(head string, num int) bool {
	for _, m := range headerNum.FindAllStringSubmatch(head, -1) {
		for _, g := range m[1:] {
			if g == "" {
				continue
			}
			if v, err := strconv.Atoi(g); err == nil && v == num {
				return true
			}
		}
	}
	return false
}
