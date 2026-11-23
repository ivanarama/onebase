package interpreter_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// profileSender — отправитель с поддержкой профиля: запоминает, что пришло.
type profileSender struct {
	stubSender
	profile *interpreter.EmailProfile
	msg     interpreter.EmailMessage
	sent    int
}

func (s *profileSender) SendMessage(p *interpreter.EmailProfile, msg interpreter.EmailMessage) error {
	s.profile, s.msg = p, msg
	s.sent++
	return nil
}

// ОтправитьПисьмо(Кому, Тема, Текст, Параметры): профиль SMTP из DSL, несколько
// получателей, копия, HTML, вложение-xlsx из ВыгрузитьВExcel (base64) и имя
// отправителя — как письмо бригадам в 1С.
func TestEmailShorthandWithProfile(t *testing.T) {
	s := &profileSender{}
	src := `Процедура Тест()
  Данные = Новый Массив;
  Заголовки = Новый Массив; Заголовки.Добавить("Номер"); Заголовки.Добавить("Адрес");
  Данные.Добавить(Заголовки);
  Строка1 = Новый Массив; Строка1.Добавить("Пл8000641"); Строка1.Добавить("ул. Ленина, 1");
  Данные.Добавить(Строка1);
  Вложения = Новый Массив;
  Вложения.Добавить(Новый Структура("ИмяФайла, Данные, Base64", "заявки.xlsx", ВыгрузитьВExcel(Данные, "Заявки"), Истина));
  Профиль = Новый Структура("Сервер, Порт, Пользователь, Пароль, Защита, Таймаут, Отправитель, ИмяОтправителя",
    "smtp.iceberg.test", 587, "robot", "Секрет", "STARTTLS", 20, "robot@iceberg.test", "Робот Айсберг-1С");
  ОтправитьПисьмо("b1@iceberg.test; Бригада 2 <b2@iceberg.test>", "Заявки на завтра", "Список во вложении",
    Новый Структура("HTML, Копия, Вложения, Профиль", "<p>Список</p>", "control@iceberg.test", Вложения, Профиль));
КонецПроцедуры`
	runHTTPSrc(t, src, interpreter.NewEmailFunctions(s, nil))
	require.Equal(t, 1, s.sent)
	require.NotNil(t, s.profile)
	assert.Equal(t, "smtp.iceberg.test", s.profile.Host)
	assert.Equal(t, 587, s.profile.Port)
	assert.Equal(t, "starttls", s.profile.Security)
	assert.Equal(t, "Робот Айсберг-1С", s.profile.FromName)
	assert.Equal(t, []string{"b1@iceberg.test", "Бригада 2 <b2@iceberg.test>"}, s.msg.To)
	assert.Equal(t, []string{"control@iceberg.test"}, s.msg.Cc)
	assert.Equal(t, "<p>Список</p>", s.msg.HTML)
	require.Len(t, s.msg.Files, 1)
	assert.Equal(t, "заявки.xlsx", s.msg.Files[0].Name)
	assert.True(t, strings.HasPrefix(string(s.msg.Files[0].Data), "PK"), "вложение — раскодированный xlsx (zip)")
	assert.Contains(t, s.msg.Files[0].MimeType, "spreadsheetml")
}

// Объект ПисьмоEmail: Профиль, Копия, ИмяОтправителя и ДобавитьВложение из данных.
func TestEmailObjectWithProfileAndDataAttachment(t *testing.T) {
	s := &profileSender{}
	src := `Процедура Тест()
  Письмо = Новый ПисьмоEmail;
  Письмо.Профиль = Новый Структура("Сервер, Пользователь, Пароль", "smtp.iceberg.test", "robot@iceberg.test", "Секрет");
  Письмо.Кому = "b1@iceberg.test, b2@iceberg.test";
  Письмо.Копия = "control@iceberg.test";
  Письмо.ИмяОтправителя = "Робот";
  Письмо.Тема = "Отчёт";
  Письмо.HTMLТело = "<b>Итоги</b>";
  Письмо.ДобавитьВложение("отчёт.txt", "строка 1");
  Письмо.Отправить();
КонецПроцедуры`
	runHTTPSrc(t, src, interpreter.NewEmailFunctions(s, nil))
	require.Equal(t, 1, s.sent)
	assert.Equal(t, "tls", s.profile.Security, "Защита по умолчанию — TLS")
	assert.Equal(t, 465, s.profile.Port, "порт по умолчанию для TLS")
	assert.Equal(t, []string{"b1@iceberg.test", "b2@iceberg.test"}, s.msg.To)
	assert.Equal(t, []string{"control@iceberg.test"}, s.msg.Cc)
	assert.Equal(t, "Робот", s.msg.FromName)
	require.Len(t, s.msg.Files, 1)
	assert.Equal(t, "строка 1", string(s.msg.Files[0].Data))
	assert.Equal(t, "text/plain", s.msg.Files[0].MimeType)
}

// Неверный профиль — ошибка с причиной, без пароля в тексте.
func TestEmailProfileErrorsHideNothingSecret(t *testing.T) {
	s := &profileSender{}
	src := `Процедура Тест()
  ОтправитьПисьмо("a@iceberg.test", "Т", "Т", Новый Структура("Профиль",
    Новый Структура("Сервер, Пароль, Защита", "smtp.iceberg.test", "Секрет-999", "КАК-НИБУДЬ")));
КонецПроцедуры`
	err := runHTTPSrcErr(t, src, interpreter.NewEmailFunctions(s, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Защита")
	assert.NotContains(t, err.Error(), "Секрет-999")
	assert.Equal(t, 0, s.sent)
}

// Песочница без сети запрещает отправку и с профилем.
func TestEmailProfileDeniedInSandbox(t *testing.T) {
	s := &profileSender{}
	deny := interpreter.NetGuard(func() error { return errors.New("сеть запрещена в песочнице") })
	src := `Процедура Тест()
  ОтправитьПисьмо("a@iceberg.test", "Т", "Т", Новый Структура("Профиль", Новый Структура("Сервер", "smtp.iceberg.test")));
КонецПроцедуры`
	err := runHTTPSrcErr(t, src, interpreter.NewEmailFunctions(s, deny))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "сеть")
	assert.Equal(t, 0, s.sent)
}
