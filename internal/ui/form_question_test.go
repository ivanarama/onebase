package ui

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// #1528 (план 158 срез A): ПоказатьВопрос — двухфазный диалог. Фаза 1 кладёт
// вопрос в ответ (клиент рисует модал), фаза 2 возвращает ответ пользователя
// событием Ответ с переменной ВопросОтвет.

func questionTestServer(t *testing.T) (*Server, *metadata.Entity) {
	t.Helper()
	return setupManagedEventsServer(t, `
Процедура КомандаНажатие()
	ПоказатьВопрос("Продолжить?", "ДаНет", "Подтверждение");
КонецПроцедуры

Процедура КомандаОтвет()
	Сообщить("Ответ: " + ВопросОтвет);
КонецПроцедуры
`, nil, []*metadata.FormElement{
		{
			Kind: metadata.FormElementButton,
			Name: "Команда",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnClick:  "КомандаНажатие",
				metadata.FormEventOnAnswer: "КомандаОтвет",
			},
		},
	})
}

func TestShowQuestion_Phase1CarriesQuestion(t *testing.T) {
	srv, ent := questionTestServer(t)
	body := url.Values{}
	body.Set("_element", "Команда")
	body.Set("_event", string(metadata.FormEventOnClick))

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if resp.Question == nil {
		t.Fatalf("response carries no question payload: %+v", resp)
	}
	if resp.Question.Text != "Продолжить?" {
		t.Fatalf("question text=%q", resp.Question.Text)
	}
	if strings.Join(resp.Question.Variants, "|") != "Да|Нет" {
		t.Fatalf("variants=%v", resp.Question.Variants)
	}
	if resp.Question.Title != "Подтверждение" {
		t.Fatalf("title=%q", resp.Question.Title)
	}
}

func TestShowQuestion_Phase2DeliversAnswer(t *testing.T) {
	srv, ent := questionTestServer(t)
	body := url.Values{}
	body.Set("_element", "Команда")
	body.Set("_event", string(metadata.FormEventOnAnswer))
	body.Set("_question_answer", "Да")

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	joined := strings.Join(resp.Messages, "\n")
	if !strings.Contains(joined, "Ответ: Да") {
		t.Fatalf("Ответ handler did not receive ВопросОтвет: %v", resp.Messages)
	}
}

func TestShowQuestion_UnknownVariantTokenFailsClosed(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура КомандаНажатие()
	ПоказатьВопрос("Продолжить?", "ЧтоНибудь");
КонецПроцедуры
`, nil, []*metadata.FormElement{
		{
			Kind: metadata.FormElementButton, Name: "Команда",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "КомандаНажатие"},
		},
	})
	body := url.Values{}
	body.Set("_element", "Команда")
	body.Set("_event", string(metadata.FormEventOnClick))

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.OK {
		t.Fatalf("unknown variant token must fail the event")
	}
	if !strings.Contains(resp.Error, "неизвестный набор вариантов") {
		t.Fatalf("error=%q", resp.Error)
	}
}

func TestExpandQuestionVariants(t *testing.T) {
	for token, want := range map[string][]string{
		"ДаНет":       {"Да", "Нет"},
		"данетотмена": {"Да", "Нет", "Отмена"},
		"ОК":          {"ОК"},
	} {
		got, err := expandQuestionVariants(any(token))
		if err != nil || strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("%q: got %v, %v", token, got, err)
		}
	}
	arr := []any{"Оформить", "Отложить"}
	got, err := expandQuestionVariants(any(arr))
	if err != nil || strings.Join(got, "|") != "Оформить|Отложить" {
		t.Fatalf("list: got %v, %v", got, err)
	}
	if _, err := expandQuestionVariants(any("мимо")); err == nil {
		t.Fatal("unknown token must fail")
	}
}
