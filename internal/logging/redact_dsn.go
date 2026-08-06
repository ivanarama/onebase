package logging

import "strings"

// RedactDSN прячет пароль в строке подключения:
//
//	postgres://user:secret@host:5432/db  → postgres://user:***@host:5432/db
//	host=... password=secret sslmode=... → host=... password=*** sslmode=...
//
// Один редактор на проект: раньше байт-в-байт одинаковые копии жили в
// internal/ui (экран «О программе») и в internal/launcher (список баз и
// конфигуратор), и любая правка в одной из них расходилась с другой. Теперь
// сюда же ходит сборщик отчёта об ошибке (план 115), где цена промаха выше:
// отчёт уходит наружу.
func RedactDSN(dsn string) string {
	// URL-форма: postgres://user:pass@host/db
	if i := strings.Index(dsn, "://"); i >= 0 {
		rest := dsn[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 {
			userPart := rest[:at]
			if colon := strings.LastIndex(userPart, ":"); colon >= 0 {
				return dsn[:i+3+colon+1] + "***" + dsn[i+3+at:]
			}
		}
	}
	// Keyword/value-форма: host=... password=secret ...
	if i := strings.Index(dsn, "password="); i >= 0 {
		end := i + len("password=")
		rest := dsn[end:]
		if sp := strings.IndexByte(rest, ' '); sp >= 0 {
			return dsn[:end] + "***" + rest[sp:]
		}
		return dsn[:end] + "***"
	}
	return dsn
}
