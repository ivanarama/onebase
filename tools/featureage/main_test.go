package main

import (
	"strings"
	"testing"
	"time"
)

// doc — миниатюрный features.md со всеми случаями, которые инструмент обязан
// различать. Пишется здесь, а не читается из репозитория: тест про правило,
// а не про текущее содержимое документа.
const doc = `# Возможности

## Старая возможность
<!-- status: testing -->
<!-- date: 2026-01-10 -->

Тело есть, заявки нет — созрела.

## Свежая возможность
<!-- status: testing -->
<!-- date: 2026-08-15 -->

Выдержка не вышла.

## Спорная возможность
<!-- status: testing -->
<!-- date: 2026-01-10 -->
<!-- issue: 777 -->

Обсуждение открыто — статус меняет человек.

## Заготовка
<!-- status: testing -->
<!-- date: 2026-01-10 -->

## Уже стабильная
<!-- status: stable -->
<!-- date: 2026-01-10 -->

Трогать нельзя.

## Без даты
<!-- status: testing -->

Возраст неизвестен.
`

func run(t *testing.T) (string, []verdict, int) {
	t.Helper()
	now, err := time.Parse("2006-01-02", "2026-08-19")
	if err != nil {
		t.Fatal(err)
	}
	return process(doc, now, 6)
}

func TestProcess_RipeSectionBecomesStable(t *testing.T) {
	out, ripe, _ := run(t)
	if len(ripe) != 2 {
		t.Fatalf("созревших разделов %d, ожидалось 2: %+v", len(ripe), ripe)
	}
	if ripe[0].title != "Старая возможность" || ripe[0].status != "stable" {
		t.Errorf("первый созревший = %+v", ripe[0])
	}
	if !strings.Contains(out, "## Старая возможность\n<!-- status: stable -->") {
		t.Error("статус старой возможности не переписан на stable")
	}
}

// Пустой раздел — не «стабильный», а «нужно описание»: пометить его stable
// значило бы объявить готовым то, чего никто не описал.
func TestProcess_EmptySectionBecomesNeedsDescription(t *testing.T) {
	out, ripe, _ := run(t)
	var found bool
	for _, v := range ripe {
		if v.title == "Заготовка" {
			found = true
			if v.status != "needs-description" {
				t.Errorf("пустой раздел → %q, ожидалось needs-description", v.status)
			}
		}
	}
	if !found {
		t.Fatal("пустой раздел не признан созревшим")
	}
	if !strings.Contains(out, "## Заготовка\n<!-- status: needs-description -->") {
		t.Error("статус заготовки не переписан")
	}
}

func TestProcess_LeavesEverythingElseAlone(t *testing.T) {
	out, _, skipped := run(t)
	if skipped != 1 {
		t.Errorf("пропущено по ссылке на заявку %d, ожидалась 1", skipped)
	}
	for _, keep := range []string{
		"## Свежая возможность\n<!-- status: testing -->",  // выдержка не вышла
		"## Спорная возможность\n<!-- status: testing -->", // открыта заявка
		"## Уже стабильная\n<!-- status: stable -->",       // уже не testing
		"## Без даты\n<!-- status: testing -->",            // возраст неизвестен
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("тронут раздел, который трогать нельзя: %q", strings.SplitN(keep, "\n", 2)[0])
		}
	}
}

// Без -apply файл не меняется вовсе — инструмент по умолчанию только отчёт.
func TestProcess_DoesNotTouchInputString(t *testing.T) {
	before := doc
	_, _, _ = run(t)
	if doc != before {
		t.Fatal("process изменил исходный текст")
	}
}
