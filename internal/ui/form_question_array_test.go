package ui

import (
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// Массив строк из DSL как набор кнопок.
//
// Документация обещает «массив строк» вторым аргументом ПоказатьВопрос, но
// билтин принимал только готовый []any. Конфигурация пишет «Новый Массив», он
// приходит сюда как *interpreter.Array — и документированный способ отвергался
// с «неизвестный набор вариантов "Массив[N]"». Свои подписи нужны там, где
// «Да/Нет» не отвечает на вопрос: «Записать на оформлении» против
// «Продолжить оформление».
func TestQuestionVariantsAcceptDSLArray(t *testing.T) {
	var payload questionPayload
	builtin := newQuestionBuiltin(&payload)

	варианты := interpreter.NewArray([]any{"Записать на оформлении", "Продолжить оформление"})
	if _, err := builtin([]any{"Не верные реквизиты", варианты}, "", 0); err != nil {
		t.Fatalf("массив DSL отвергнут: %v", err)
	}
	if len(payload.Variants) != 2 ||
		payload.Variants[0] != "Записать на оформлении" ||
		payload.Variants[1] != "Продолжить оформление" {
		t.Errorf("подписи кнопок не те: %#v", payload.Variants)
	}
}

func TestQuestionVariantsRejectEmptyDSLArray(t *testing.T) {
	var payload questionPayload
	builtin := newQuestionBuiltin(&payload)
	if _, err := builtin([]any{"Вопрос", interpreter.NewArray(nil)}, "", 0); err == nil {
		t.Error("пустой массив вариантов принят")
	}
}
