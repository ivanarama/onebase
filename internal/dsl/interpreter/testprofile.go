package interpreter

import (
	"strings"
	"time"
)

// Тест-профиль (план 108, шаг 3): подменяемые часы и моки-рекордеры внешних
// эффектов. Раннер `onebase test` инжектирует его в каждый прогон, поэтому код
// рассылок/интеграций тестируем без реального SMTP/сети/ОС/ИИ: вместо отправки
// эффект записывается, и тест ассертит «письмо ушло бы на X с вложением Y».
//
//	Часы.Установить(Дата(2026,7,29));  // ТекущаяДата/ТекущаяДатаВремя замрут
//	ПровестиРассылку();                // код, дергающий ОтправитьПисьмо
//	Утверждать.Равно(Мок.Email.Количество(), 3, "3 письма");
//	Утверждать.Равно(Мок.Email[0].Кому, "a@b.ru", "адрес");
//	Часы.Сбросить();

// ─── Часы (подменяемое время) ────────────────────────────────────────────────

// TestClock — источник времени с возможностью заморозки. nil frozen = реальное
// время. Раннер сбрасывает его между тестами.
type TestClock struct{ frozen *time.Time }

func (c *TestClock) now() time.Time {
	if c.frozen != nil {
		return *c.frozen
	}
	return time.Now()
}

func (c *TestClock) today() time.Time {
	t := c.now()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

// ClockRoot — DSL-объект Часы.
type ClockRoot struct{ clock *TestClock }

func (r *ClockRoot) Get(string) any  { return nil }
func (r *ClockRoot) Set(string, any) {}

func (r *ClockRoot) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "установить", "set":
		t, ok := toTime(args, 0)
		if !ok {
			panic(userError{Msg: "Часы.Установить: ожидается дата"})
		}
		frozen := t
		r.clock.frozen = &frozen
		return nil
	case "сбросить", "reset":
		r.clock.frozen = nil
		return nil
	}
	panic(userError{Msg: "Часы: неизвестный метод «" + method + "» (доступны Установить, Сбросить)"})
}

// ─── Мок (рекордеры внешних эффектов) ─────────────────────────────────────────

// MockRoot — DSL-объект Мок. Его поля Email/Http/ОС/ИИ — живые массивы
// записей вызовов (`*Array` из `*MapThis`), поэтому в тесте работают и
// индексация (Мок.Email[0].Кому), и методы массива (Количество/Очистить).
type MockRoot struct {
	email *Array
	http  *Array
	exec  *Array
	llm   *Array
	// pauses хранит сами выдержки: одного сдвига замороженных часов мало,
	// поскольку тестовый код мог передвинуть их вручную.
	pauses *Array
}

func newMockRoot() *MockRoot {
	return &MockRoot{email: &Array{}, http: &Array{}, exec: &Array{}, llm: &Array{}, pauses: &Array{}}
}

func (m *MockRoot) Get(name string) any {
	switch strings.ToLower(name) {
	case "email", "почта":
		return m.email
	case "http":
		return m.http
	case "ос", "os", "команды":
		return m.exec
	case "ии", "ai", "llm":
		return m.llm
	case "паузы", "pauses", "sleeps":
		return m.pauses
	}
	return nil
}

func (m *MockRoot) Set(string, any) {}

func (m *MockRoot) reset() {
	m.email.items = nil
	m.http.items = nil
	m.exec.items = nil
	m.llm.items = nil
	m.pauses.items = nil
}

func recordCall(arr *Array, fields map[string]any) {
	arr.items = append(arr.items, &MapThis{M: fields})
}

// mockEmailSender записывает письма вместо отправки. Реализует EmailSender и
// EmailAttachmentSender, поэтому подходит и shorthand'у, и объекту ПисьмоEmail.
type mockEmailSender struct{ arr *Array }

func (s *mockEmailSender) Configured() bool { return true }

func (s *mockEmailSender) Send(to, subject, textBody, htmlBody string) error {
	recordCall(s.arr, emailFields(to, subject, textBody, htmlBody, nil))
	return nil
}

func (s *mockEmailSender) SendWithAttachments(to, subject, textBody, htmlBody string, files []EmailAttachment) error {
	recordCall(s.arr, emailFields(to, subject, textBody, htmlBody, files))
	return nil
}

func emailFields(to, subject, text, html string, files []EmailAttachment) map[string]any {
	names := &Array{}
	for _, f := range files {
		names.items = append(names.items, f.Name)
	}
	return map[string]any{
		"Кому": to, "Тема": subject, "Текст": text, "HTMLТело": html,
		"To": to, "Subject": subject, "Text": text,
		"Вложений":      float64(len(files)),
		"ИменаВложений": names,
	}
}

// ─── TestProfile ──────────────────────────────────────────────────────────────

// TestProfile объединяет часы и моки одного прогона тестов.
type TestProfile struct {
	clock  *TestClock
	mock   *MockRoot
	sender *mockEmailSender
}

// NewTestProfile создаёт профиль для раннера тестов.
func NewTestProfile() *TestProfile {
	m := newMockRoot()
	return &TestProfile{
		clock:  &TestClock{},
		mock:   m,
		sender: &mockEmailSender{arr: m.email},
	}
}

// Reset очищает рекордеры и сбрасывает часы — вызывать перед каждым тестом,
// чтобы Мок.* и время не протекали между тестами.
func (p *TestProfile) Reset() {
	p.mock.reset()
	p.clock.frozen = nil
}

// Vars — переменные тест-профиля для инъекции в прогон (поверх стандартных).
// Переопределяют встроенные функции даты/сети/ОС/ИИ рекордерами и добавляют
// объекты Часы и Мок. Инъекция идёт последней, поэтому перекрывает штатные
// функции окружения.
func (p *TestProfile) Vars() map[string]any {
	nowFn := BuiltinFunc(func([]any, string, int) (any, error) { return p.clock.now(), nil })
	// Выдержка под замороженными часами двигает время, а не тратит его: backoff
	// проверяется headless и мгновенно (issue #708).
	sleepFn := newFrozenClockSleepBuiltin(p.clock, p.mock.pauses)
	todayFn := BuiltinFunc(func([]any, string, int) (any, error) { return p.clock.today(), nil })

	// Почта: shorthand пишет запись напрямую; объект ПисьмоEmail строится из
	// реального dslEmail с записывающим отправителем и nil-guard — так вся
	// валидация и логика вложений остаётся настоящей.
	sendEmail := BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		recordCall(p.mock.email, emailFields(strArg(args, 0), strArg(args, 1), strArg(args, 2), "", nil))
		return nil, nil
	})
	emailFactory := func(args []any) any {
		return &dslEmail{sender: p.sender, guard: nil}
	}

	httpGet := BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		recordCall(p.mock.http, map[string]any{"Метод": "GET", "URL": strArg(args, 0)})
		return &dslHTTPResponse{statusCode: 200}, nil
	})
	httpPost := BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		recordCall(p.mock.http, map[string]any{"Метод": "POST", "URL": strArg(args, 0), "Тело": strArg(args, 1)})
		return &dslHTTPResponse{statusCode: 200}, nil
	})

	execFn := BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		recordCall(p.mock.exec, map[string]any{"Команда": strArg(args, 0)})
		return &MapThis{M: map[string]any{
			"КодВозврата": float64(0), "СтандартныйВывод": "", "ОшибочныйВывод": "", "Завершилась": true,
		}}, nil
	})

	aiFn := BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		recordCall(p.mock.llm, map[string]any{"Запрос": strArg(args, 0)})
		return "", nil
	})

	return map[string]any{
		"Часы":  &ClockRoot{clock: p.clock},
		"Clock": &ClockRoot{clock: p.clock},
		"Мок":   p.mock,
		"Mock":  p.mock,

		// подменяемое время
		"ТекущаяДатаВремя": nowFn, "Now": nowFn,
		"ТекущаяДата": todayFn, "Today": todayFn,
		"Приостановить": sleepFn, "Пауза": sleepFn, "Подождать": sleepFn,
		"Sleep": sleepFn, "Wait": sleepFn,

		// почта
		"ОтправитьПисьмо": sendEmail, "SendEmail": sendEmail,
		"__factory_ПисьмоEmail": emailFactory, "__factory_EmailMessage": emailFactory,

		// сеть
		"HTTPПолучить": httpGet, "HTTPGet": httpGet,
		"HTTPОтправить": httpPost, "HTTPPost": httpPost,

		// команды ОС
		"ВыполнитьКоманду": execFn,

		// ИИ
		"ЗапросИИ": aiFn,
	}
}
