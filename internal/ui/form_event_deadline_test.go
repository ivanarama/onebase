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

// Предел конкурентности: когда все слоты заняты, событие получает 429, а не
// встаёт в очередь на соединение к БД.
func TestСобытиеФормы_ПределКонкурентности(t *testing.T) {
	s, ent := setupManagedEventsServer(t, `
Процедура Долго()
	Приостановить(5);
КонецПроцедуры
`, map[metadata.FormEventType]string{}, []*metadata.FormElement{{
		Kind:     metadata.FormElementButton,
		Name:     "Кнопка",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Долго"},
	}})
	s.cfg.Limits.RequestTimeoutSec = 30
	s.cfg.Limits.ProcessorConcurrency = 1

	body := url.Values{"_element": {"Кнопка"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}}
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		executeFormEventRaw(t, s, ent, body)
		close(done)
	}()
	<-started
	// Даём первому запросу занять слот.
	time.Sleep(300 * time.Millisecond)

	rec := executeFormEventRaw(t, s, ent, body)
	if rec.Code != 429 {
		t.Errorf("второе событие при занятом слоте: код %d, ожидался 429", rec.Code)
	}
	<-done
}
