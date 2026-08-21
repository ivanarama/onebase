package main

import "testing"

// Заявки-образцы: одна «забытая» русским словом, одна закрытая по-английски,
// одна сводная (её упоминают все подряд, и это норма).
var openIssues = []issue{
	{Number: 611, Title: "disableUnreadableTOTP не вызывается в проде", URL: "u/611"},
	{Number: 826, Title: "Аудит роняет запись объекта", URL: "u/826"},
	{Number: 962, Title: "Ревью недели", URL: "u/962"},
}

func TestRussianKeywordIsTheTail(t *testing.T) {
	// Ровно тот случай, ради которого сверка заведена: автор написал по-русски,
	// GitHub слова не понял, заявка осталась открытой.
	prs := []pull{{
		Number: 700,
		Title:  "fix(auth): отключение нечитаемого TOTP",
		Body:   "Закрывает #611. Проверка идёт публичной дверью.",
		URL:    "u/pr/700",
	}}

	declared, mentioned := analyze(openIssues, prs)
	if len(declared) != 1 {
		t.Fatalf("ожидалась одна заявка с заявленным закрытием, получено %d", len(declared))
	}
	if declared[0].issue.Number != 611 {
		t.Fatalf("нашлась заявка #%d, ожидалась #611", declared[0].issue.Number)
	}
	if declared[0].english {
		t.Fatal("«Закрывает» помечено английским словом — тогда отчёт соврёт про причину")
	}
	if len(mentioned) != 0 {
		t.Fatalf("заявка не должна попасть в обе корзины разом: %d упоминаний", len(mentioned))
	}
}

func TestEnglishKeywordOnOpenIssueIsSuspicious(t *testing.T) {
	// Английское слово GitHub понимает. Если заявка всё же открыта — это другой
	// отказ (PR влит не в ту ветку), и объяснение обязано быть другим.
	prs := []pull{{
		Number: 830,
		Title:  "fix(storage): bestEffort",
		Body:   "Fixes #826",
		URL:    "u/pr/830",
	}}

	declared, _ := analyze(openIssues, prs)
	if len(declared) != 1 || declared[0].issue.Number != 826 {
		t.Fatalf("ожидалась #826, получено %+v", declared)
	}
	if !declared[0].english {
		t.Fatal("«Fixes» не распознано английским — отчёт объяснит причину неверно")
	}
	if got := reason(declared[0]); got == reason(finding{}) {
		t.Fatal("объяснение для английского слова совпало с объяснением для русского")
	}
}

func TestMentionIsNotATail(t *testing.T) {
	// Сводную заявку упоминает каждый второй PR. Если такие попадут в первую
	// корзину, отчёт станет шумом и его перестанут читать.
	prs := []pull{{
		Number: 1042,
		Title:  "fix(storage): страховка перечислений",
		Body:   "Находка Н2 из #962. Заявку не закрывает: там ещё процессные пункты.",
		URL:    "u/pr/1042",
	}}

	declared, mentioned := analyze(openIssues, prs)
	if len(declared) != 0 {
		t.Fatalf("упоминание без ключевого слова принято за закрытие: %+v", declared)
	}
	if len(mentioned) != 1 || mentioned[0].issue.Number != 962 {
		t.Fatalf("ожидалось одно упоминание #962, получено %+v", mentioned)
	}
}

func TestClosedIssueIsNotReported(t *testing.T) {
	// В списке открытых заявок #999 нет — значит, она закрыта, и это норма,
	// а не находка.
	prs := []pull{{Number: 1000, Title: "feat: что-то", Body: "Fixes #999", URL: "u/pr/1000"}}

	declared, mentioned := analyze(openIssues, prs)
	if len(declared) != 0 || len(mentioned) != 0 {
		t.Fatalf("закрытая заявка попала в отчёт: declared=%+v mentioned=%+v", declared, mentioned)
	}
}

func TestSelfReferenceIgnored(t *testing.T) {
	// PR, чей номер совпал с номером открытой заявки, не должен «закрывать сам
	// себя»: номера PR и заявок в GitHub из одной последовательности.
	prs := []pull{{Number: 962, Title: "PR #962", Body: "Fixes #962", URL: "u/pr/962"}}

	declared, mentioned := analyze(openIssues, prs)
	if len(declared) != 0 || len(mentioned) != 0 {
		t.Fatalf("PR сослался сам на себя и попал в отчёт: %+v %+v", declared, mentioned)
	}
}

func TestOldestClaimWins(t *testing.T) {
	// gh отдаёт PR от свежих к старым. Показать надо тот PR, который заявил
	// закрытие раньше: именно с него заявка «сделана».
	prs := []pull{
		{Number: 900, Title: "поздний", Body: "Закрывает #611", URL: "u/pr/900"},
		{Number: 700, Title: "ранний", Body: "Закрывает #611", URL: "u/pr/700"},
	}

	declared, _ := analyze(openIssues, prs)
	if len(declared) != 1 {
		t.Fatalf("одна заявка должна попасть в отчёт один раз, получено %d", len(declared))
	}
	if declared[0].pr.Number != 700 {
		t.Fatalf("показан PR #%d, ожидался ранний #700", declared[0].pr.Number)
	}
}

func TestRussianVariantsRecognized(t *testing.T) {
	for _, phrase := range []string{
		"Закрывает #611", "закрыто #611", "Исправляет #611",
		"Решает #611", "чинит #611", "Закрывает: #611",
	} {
		declared, _ := analyze(openIssues, []pull{{Number: 700, Body: phrase}})
		if len(declared) != 1 {
			t.Errorf("формулировка %q не распознана как заявка о закрытии", phrase)
		}
	}
}
