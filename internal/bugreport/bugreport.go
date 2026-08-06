// Package bugreport собирает отчёт об ошибке, который пользователь отправляет
// разработчику сам — файлом или текстом (план 115).
//
// Платформа ничего не отправляет по сети: ни приёмника, ни телеметрии, ни
// секретов в бинаре. Это единственный вариант, который работает в закрытом
// контуре (issue #299) и не требует от пользователя аккаунта на GitHub —
// ровно та проблема, ради которой пакет и появился.
//
// Сборка (Markdown) намеренно отделена от доставки (WriteBundle): онлайн-
// транспорт, если он когда-нибудь понадобится, надстраивается сверху без
// переделки.
package bugreport

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/incident"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/version"
)

// Env — окружение, в котором воспроизвелась ошибка.
type Env struct {
	Platform string // "onebase build-689"
	Commit   string // короткий SHA
	Date     string // дата коммита, дд.мм.гг
	Modified bool   // сборка из грязного дерева
	OS       string // "windows/amd64"
}

// CurrentEnv читает окружение процесса. Ничего не спрашивает у сети и у базы.
func CurrentEnv() Env {
	return Env{
		Platform: "onebase " + version.String(),
		Commit:   version.Commit(),
		Date:     version.CommitDate(),
		Modified: version.Modified(),
		OS:       runtime.GOOS + "/" + runtime.GOARCH,
	}
}

func (e Env) String() string {
	s := e.Platform
	if e.Date != "" {
		s += " · " + e.Date
	}
	if e.Commit != "" {
		s += " · " + e.Commit
	}
	if e.Modified {
		s += " · сборка из изменённого дерева"
	}
	return s
}

// Contacts — куда отправлять готовый отчёт.
type Contacts struct {
	// App — контакт поддержки конкретной конфигурации (config/app.yaml →
	// support). Идёт первым: до автора платформы пользователю обычно не надо.
	App string
	// Platform — контакт разработчика платформы для тех, у кого нет аккаунта
	// GitHub. Пусто — показываем только трекер.
	Platform string
	// IssuesURL — трекер платформы. Требует аккаунта, о чём честно пишем.
	IssuesURL string
}

// Any сообщает, есть ли хоть один адрес — иначе блок «Куда отправить» не нужен.
func (c Contacts) Any() bool { return c.App != "" || c.Platform != "" || c.IssuesURL != "" }

// PlatformContacts возвращает контакты платформы (без контакта конфигурации).
func PlatformContacts(app string) Contacts {
	return Contacts{App: strings.TrimSpace(app), Platform: version.SupportContact, IssuesURL: version.IssuesURL}
}

// Input — всё, из чего собирается отчёт.
type Input struct {
	Did      string // что делал
	Expected string // что ожидал
	Got      string // что получилось

	Incident *incident.Record // выбранный инцидент, может быть nil

	AppName      string // имя конфигурации
	AppVersion   string
	ConfigSource string // "file" | "database"
	DBKind       string // "sqlite" | "postgres"

	// LogTail — хвост журнала. Пусто — журнал не прикладываем (так отчёт
	// выглядит у обычного пользователя базы: журнал видит только админ).
	LogTail string

	Contacts Contacts
	Now      time.Time // для воспроизводимости в тестах; ноль = time.Now()
}

// Markdown собирает тело отчёта.
//
// Всё, что попадает в текст, проходит через Redact. Это не гарантия — текст
// ошибки может содержать данные, введённые пользователем, и вычистить их
// автоматически нельзя. Настоящая защита — предпросмотр: пользователь видит
// готовый отчёт целиком и правит его перед отправкой.
func Markdown(in Input) string {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	var b strings.Builder
	b.WriteString("# OneBase — сообщение об ошибке\n\n")
	b.WriteString("Дата: " + now.Format("2006-01-02 15:04:05") + "\n\n")

	b.WriteString(field("Что делал", in.Did))
	b.WriteString(field("Что ожидал", in.Expected))
	b.WriteString(field("Что получилось", in.Got))

	if rec := in.Incident; rec != nil {
		b.WriteString("\n## Инцидент " + rec.ID + "\n\n")
		b.WriteString("```\n")
		b.WriteString("Время:  " + rec.Time.Format("2006-01-02 15:04:05") + "\n")
		if rec.Where != "" {
			b.WriteString("Место:  " + Redact(rec.Where) + "\n")
		}
		if rec.Text != "" {
			b.WriteString("Ошибка: " + Redact(rec.Text) + "\n")
		}
		if rec.Stack != "" {
			b.WriteString("\n" + Redact(rec.Stack) + "\n")
		}
		b.WriteString("```\n")
	}

	env := CurrentEnv()
	b.WriteString("\n## Окружение\n\n")
	b.WriteString("```\n")
	b.WriteString("Платформа:    " + env.String() + "\n")
	b.WriteString("ОС:           " + env.OS + "\n")
	if in.DBKind != "" {
		b.WriteString("СУБД:         " + dbKindLabel(in.DBKind) + "\n")
	}
	if in.AppName != "" {
		line := in.AppName
		if in.AppVersion != "" {
			line += " " + in.AppVersion
		}
		if in.ConfigSource != "" {
			line += " (" + configSourceLabel(in.ConfigSource) + ")"
		}
		b.WriteString("Конфигурация: " + line + "\n")
	}
	b.WriteString("```\n")

	if tail := strings.TrimSpace(in.LogTail); tail != "" {
		b.WriteString("\n## Журнал\n\n")
		b.WriteString("```\n" + Redact(tail) + "\n```\n")
	}

	if c := contactsBlock(in.Contacts); c != "" {
		b.WriteString("\n## Куда отправить\n\n" + c)
	}
	return b.String()
}

func field(label, value string) string {
	value = strings.TrimSpace(Redact(value))
	if value == "" {
		value = "—"
	}
	return "**" + label + ":** " + value + "\n\n"
}

func dbKindLabel(kind string) string {
	if strings.EqualFold(kind, "sqlite") {
		return "SQLite"
	}
	return "PostgreSQL"
}

func configSourceLabel(src string) string {
	if src == "database" {
		return "в базе данных"
	}
	return "файлы"
}

// contactsBlock печатает адреса в порядке «сначала тот, до кого дойдут».
func contactsBlock(c Contacts) string {
	var lines []string
	if c.App != "" {
		lines = append(lines, "- Поддержка конфигурации: "+c.App)
	}
	if c.Platform != "" {
		lines = append(lines, "- Разработчик платформы: "+c.Platform)
	}
	if c.IssuesURL != "" {
		lines = append(lines, "- Трекер платформы (нужен аккаунт GitHub): "+c.IssuesURL)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// Redact вычищает из текста то, что распознаётся машинно: пароль в строке
// подключения и значения чувствительных параметров в URL.
//
// Работает построчно, потому что журнал — это много строк, и DSN встречается
// в любой из них.
func Redact(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = oblog.RedactURI(oblog.RedactDSN(ln))
	}
	return strings.Join(lines, "\n")
}

// FileName возвращает имя файла отчёта: onebase-report-20260807-143211.md
func FileName(now time.Time, ext string) string {
	if now.IsZero() {
		now = time.Now()
	}
	return fmt.Sprintf("onebase-report-%s.%s", now.Format("20060102-150405"), strings.TrimPrefix(ext, "."))
}
