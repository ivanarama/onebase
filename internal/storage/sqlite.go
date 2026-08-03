package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"github.com/ivantit66/onebase/internal/fsmode"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	sqlite "modernc.org/sqlite"
)

// init регистрирует Unicode-aware функцию ob_lower для SQLite. Встроенная
// SQLite LOWER() приводит к нижнему регистру только ASCII, поэтому отборы и
// поиск по кириллице получались регистрозависимыми. ob_lower использует
// strings.ToLower (полная таблица Unicode) и применяется в LowerLike SQLite-
// диалекта. Регистрация глобальна и действует на все коннекты, открытые позже.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("ob_lower", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if len(args) != 1 || args[0] == nil {
			return nil, nil
		}
		switch v := args[0].(type) {
		case string:
			return strings.ToLower(v), nil
		case []byte:
			return strings.ToLower(string(v)), nil
		default:
			return v, nil
		}
	})
}

// ConnectSQLite opens (or creates) a SQLite database file at the given path
// and applies pragmas that match the project's operational profile:
//   - WAL journal: concurrent readers don't block on a writer.
//   - synchronous=NORMAL: balance durability/perf.
//   - foreign_keys=ON: enforced FK constraints (SQLite default is off).
//   - busy_timeout=5000: short retry window for concurrent writes.
//   - cache_size=-64000: 64 MiB page cache.
//
// filesDir defaults to <home>/.onebase/files/<basename-without-ext>.
func ConnectSQLite(ctx context.Context, dbPath string) (*DB, error) {
	if dbPath == "" {
		return nil, i18nerr.New("storage: sqlite: пустой путь к файлу базы данных")
	}

	// In-memory база (:memory: / file::memory:) — чистая БД на процесс, без
	// файла на диске. Нужна раннеру тестов (onebase test --sqlite :memory:) и
	// песочнице eval: каждый запуск стартует с пустой схемы, ничего не оседает
	// в рабочей папке. Пропускаем работу с ФС (Abs/MkdirAll/probe создали бы
	// мусорный файл с именем «:memory:»); вложения/файлы — во временную папку.
	// Один коннект в пуле (см. ниже) держит БД живой на всё время работы.
	inMemory := isInMemorySQLite(dbPath)

	var dsnPath, filesDir string
	if inMemory {
		tmp, terr := os.MkdirTemp("", "onebase-mem-files-")
		if terr != nil {
			return nil, i18nerr.Wrapf(terr, "storage: sqlite: не удалось создать временную папку файлов")
		}
		dsnPath = dbPath
		filesDir = tmp
	} else {
		// Ensure absolute path — relative paths fail on Windows when the working
		// directory is restricted (e.g. Program Files).
		absPath, err := filepath.Abs(dbPath)
		if err != nil {
			return nil, i18nerr.Wrapf(err, "storage: sqlite: не удалось получить абсолютный путь %q", dbPath)
		}
		dbPath = absPath

		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
			return nil, i18nerr.Wrapf(err, "storage: sqlite: не удалось создать папку %q", dir)
		}

		// Quick write-permission check before handing path to SQLite.
		probe := filepath.Join(dir, ".onebase_probe")
		if f, ferr := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600); ferr != nil {
			return nil, i18nerr.Wrapf(ferr, "storage: sqlite: нет прав на запись в папку %q", dir)
		} else {
			discardPartial(f, probe)
		}

		// modernc.org/sqlite uses "sqlite" driver name (not "sqlite3").
		// On Windows use forward slashes — the transpiled SQLite C code handles
		// them better than backslashes in some path formats.
		dsnPath = filepath.ToSlash(dbPath)
		filesDir = defaultFilesDirForSQLite(dbPath)
	}

	conn, err := sql.Open("sqlite", dsnPath)
	if err != nil {
		return nil, fmt.Errorf("storage: sqlite: open %q: %w", dbPath, err)
	}

	// Один коннект в пуле — критично для SQLite. PRAGMA настройки
	// (busy_timeout, journal_mode и т.п.) применяются per-connection, а
	// database/sql ленивo открывает дополнительные коннекты при параллельной
	// нагрузке: они не получают прагмы и сразу падают с SQLITE_BUSY. SQLite
	// всё равно single-writer на запись, а на чтение в WAL-режиме одного
	// коннекта достаточно (внутри SQLite читатели не блокируют друг друга).
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)

	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("storage: sqlite: ping %q: %w", dbPath, err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"PRAGMA cache_size=-64000",
	}
	for _, p := range pragmas {
		if _, err := conn.ExecContext(ctx, p); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("storage: sqlite: %s: %w", p, err)
		}
	}

	return &DB{
		sqlDB:    conn,
		filesDir: filesDir,
		dialect:  SQLiteDialect{},
	}, nil
}

// isInMemorySQLite распознаёт in-memory формы пути SQLite: канонический
// «:memory:» и DSN-формы, где имя файла пустое, а память задана параметром
// (file::memory:… / …mode=memory…).
func isInMemorySQLite(dbPath string) bool {
	if dbPath == ":memory:" {
		return true
	}
	low := strings.ToLower(dbPath)
	if strings.HasPrefix(low, "file::memory:") {
		return true
	}
	return strings.HasPrefix(low, "file:") && strings.Contains(low, "mode=memory")
}

// ValidateQuery компилирует (PREPARE) SQL без исполнения — для статической
// валидации запросов против схемы (onebase check, п.45). Ловит то, что пропускает
// компиляция DSL→SQL: no such table (VT без скобок), ambiguous column (авто-JOIN),
// неизвестные ключевые слова (near "…": syntax error). Только SQLite; на других
// бэкендах возвращает nil (валидация не выполняется).
func (db *DB) ValidateQuery(ctx context.Context, sqlText string) error {
	if db.sqlDB == nil {
		return nil
	}
	stmt, err := db.sqlDB.PrepareContext(ctx, sqlText)
	if err != nil {
		return err
	}
	return stmt.Close()
}

func defaultFilesDirForSQLite(dbPath string) string {
	base := filepath.Base(dbPath)
	// strip .db / .sqlite / .sqlite3 if present
	for _, ext := range []string{".db", ".sqlite", ".sqlite3"} {
		if len(base) > len(ext) && base[len(base)-len(ext):] == ext {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".onebase", "files", base)
}
