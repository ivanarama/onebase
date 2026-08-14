package ui

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Обработчик события управляемой формы обязан отрубаться по дедлайну и
// подчиняться пределу конкурентности (#865).
//
// handleProcessorFormEvent из того же PR #735 и то и другое получил, а
// handleManagedFormEvent — самый обычный путь, кнопка на форме объекта —
// исполнял DSL напрямую s.interp.Run: без предела времени вовсе и без слота
// операций. Один обработчик с Приостановить(300) занимал соединение и держал
// пользователя пять минут.

func TestСобытиеФормы_ОтрубаетсяПоДедлайну(t *testing.T) {
	s, ent := setupManagedEventsServer(t, `
Процедура Долго()
	Приостановить(300);
КонецПроцедуры
`, map[metadata.FormEventType]string{}, []*metadata.FormElement{{
		Kind:     metadata.FormElementButton,
		Name:     "Кнопка",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Долго"},
	}})
	// Предел берётся из общего лимита запроса.
	s.cfg.Limits.RequestTimeoutSec = 1

	body := url.Values{"_element": {"Кнопка"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}}
	start := time.Now()
	rec := executeFormEventRaw(t, s, ent, body)
	elapsed := time.Since(start)

	if elapsed > 30*time.Second {
		t.Fatalf("обработчик выполнялся %v — дедлайна нет", elapsed)
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.Error == "" {
		t.Fatalf("Приостановить(300) прошло успешно за %v — предела нет", elapsed)
	}
	if !strings.Contains(resp.Error, "врем") {
		t.Errorf("ошибка не объясняет причину: %q", resp.Error)
	}
}

// Предел конкурентности: когда слот занят, событие получает 429, а не встаёт
// в очередь на соединение к БД.
//
// Слот занимается напрямую через лимитер, а не вторым живым запросом: гнать
// два прикладных DSL параллельно ради проверки гейта — значит проверять заодно
// потокобезопасность фикстуры, и под -race тест падал именно на ней, а не на
// предмете проверки.
func TestСобытиеФормы_ПределКонкурентности(t *testing.T) {
	s, ent := setupManagedEventsServer(t, `
Процедура Быстро()
	Сообщить("ok");
КонецПроцедуры
`, map[metadata.FormEventType]string{}, []*metadata.FormElement{{
		Kind:     metadata.FormElementButton,
		Name:     "Кнопка",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Быстро"},
	}})
	s.cfg.Limits.ProcessorConcurrency = 1

	body := url.Values{"_element": {"Кнопка"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}}

	// Пока слот свободен — событие проходит.
	if rec := executeFormEventRaw(t, s, ent, body); rec.Code != 200 {
		t.Fatalf("при свободном слоте код %d, ожидался 200", rec.Code)
	}

	s.ops = newOperationLimiter()
	release, ok := s.ops.tryAcquire(opFormEvent, 1)
	if !ok {
		t.Fatal("не удалось занять слот в подготовке теста")
	}
	defer release()

	if rec := executeFormEventRaw(t, s, ent, body); rec.Code != 429 {
		t.Errorf("при занятом слоте код %d, ожидался 429 — предела конкурентности нет", rec.Code)
	}
}
