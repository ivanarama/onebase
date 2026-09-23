package pipelinecontract

import (
	"strings"
	"testing"
)

// Грамматика раздела «Хвост» — контракт между REVIEW (пишет) и TAIL (разбирает),
// и до #1360 он был описан только с одной стороны. Что делать со строкой,
// которая не `[заявка]` и не `[выброс]`, не говорил никто, а оба разумных
// прочтения плохи: fail closed без объявления теряет весь хвост PR молча по
// истечении 14-дневного окна, а «тихо пропустить» теряет перенесённый пункт и
// при этом отчитывается «Хвост разобран».
//
// Выбран вариант 1 (комментарий человека в #1360): строгая грамматика и fail
// closed на обеих сторонах.

// modeledTailSectionLine — класс строки внутри раздела «Хвост».
type modeledTailSectionLine int

const (
	tailLineEmpty        modeledTailSectionLine = iota
	tailLineItem                                // «1. [заявка] …» / «2. [выброс] …»
	tailLineContinuation                        // продолжение пункта: начинается с отступа
	tailLineDash                                // одиночный прочерк вместо списка
	tailLineForeign                             // всё остальное — нарушение контракта
)

func modeledClassifyTailLine(line string) modeledTailSectionLine {
	if strings.TrimSpace(line) == "" {
		return tailLineEmpty
	}
	if strings.TrimSpace(line) == "—" {
		return tailLineDash
	}
	indented := line != strings.TrimLeft(line, " \t")
	body := strings.TrimLeft(line, " \t")

	// Пункт: номер, точка, пробел и class-token в квадратных скобках.
	digits := 0
	for digits < len(body) && body[digits] >= '0' && body[digits] <= '9' {
		digits++
	}
	if digits > 0 && strings.HasPrefix(body[digits:], ". ") {
		rest := body[digits+2:]
		if strings.HasPrefix(rest, "[заявка] ") || strings.HasPrefix(rest, "[выброс] ") {
			return tailLineItem
		}
	}
	if indented {
		return tailLineContinuation
	}
	return tailLineForeign
}

// modeledTailSectionValid — раздел разбирается, только если каждая непустая
// строка принадлежит пункту. Прочерк допустим лишь при пустом хвосте, а
// продолжение — лишь после уже начатого пункта: иначе первый же абзац перед
// списком проехал бы как «продолжение» ничего.
func modeledTailSectionValid(section []string, tailCount int) bool {
	items := 0
	dash := false
	for _, line := range section {
		switch modeledClassifyTailLine(line) {
		case tailLineEmpty:
		case tailLineItem:
			items++
		case tailLineContinuation:
			if items == 0 {
				return false
			}
		case tailLineDash:
			dash = true
		default:
			return false
		}
	}
	if dash && (items > 0 || tailCount != 0) {
		return false
	}
	return true
}

func TestTailSectionGrammarFailsClosedOnForeignLine(t *testing.T) {
	valid := []string{
		"1. [заявка] дубль проверки → заголовок: «Свернуть дубль»",
		"2. [выброс] вкусовое — не стоит заявки",
	}
	if !modeledTailSectionValid(valid, 1) {
		t.Fatal("правильный раздел признан нарушением")
	}

	// Тот самый живой случай из заявки: свободный абзац после пунктов.
	stray := append(append([]string{}, valid...),
		"", "Отдельно и мимо блокирующего, поскольку в круге 1 это уже разобрано: …")
	if modeledTailSectionValid(stray, 1) {
		t.Error("свободный абзац в хвосте прошёл как валидный раздел")
	}

	// Перенос пункта без отступа неотличим от чужого абзаца — и это тоже стоп,
	// а не молчаливая потеря половины пункта.
	wrapped := []string{
		"1. [заявка] очень длинная суть, которая",
		"не влезла в строку → заголовок: «Что-то»",
	}
	if modeledTailSectionValid(wrapped, 1) {
		t.Error("перенос без отступа принят: пункт потерялся бы молча")
	}

	// С отступом тот же перенос — законное продолжение пункта.
	indented := []string{
		"1. [заявка] очень длинная суть, которая",
		"   влезла с отступом → заголовок: «Что-то»",
	}
	if !modeledTailSectionValid(indented, 1) {
		t.Error("продолжение пункта с отступом отвергнуто")
	}

	// Продолжение до первого пункта — не продолжение, а чужой текст.
	orphan := []string{"   абзац до всякого пункта", "1. [заявка] суть → заголовок: «X»"}
	if modeledTailSectionValid(orphan, 1) {
		t.Error("отступ перед первым пунктом принят за продолжение")
	}
}

func TestTailSectionDashOnlyWithEmptyTail(t *testing.T) {
	if !modeledTailSectionValid([]string{"—"}, 0) {
		t.Error("прочерк при pp:tail=0 отвергнут")
	}
	if modeledTailSectionValid([]string{"—"}, 2) {
		t.Error("прочерк принят при непустом pp:tail")
	}
	if modeledTailSectionValid([]string{"1. [заявка] суть → заголовок: «X»", "—"}, 1) {
		t.Error("прочерк принят вместе с пунктами")
	}
}

// Грамматика обязана быть описана в ОБЕИХ процедурах: контракт, записанный
// только у одной стороны, — ровно та поломка, из-за которой заведена #1360.
func TestTailSectionGrammarStatedByBothSides(t *testing.T) {
	consumer := repositoryFile(t, ".claude", "skills", "tail-issues", "SKILL.md")
	requireAll(t, consumer, "[выброс]", "fail closed", "с отступа", "Вердикт:")

	producer := skill(t, "review-queue")
	requireAll(t, producer, "Хвост:", "с отступом", "pp:tail=0", "Свободный абзац")
}
