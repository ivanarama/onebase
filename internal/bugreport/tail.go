package bugreport

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// StartupLogLines — сколько строк стартового следа кладём в отчёт. Файл
// однострочный на запуск, поэтому десятка хватает на историю последних запусков.
const StartupLogLines = 20

// TailFile возвращает последние lines строк файла, читая не более maxBytes с
// конца. Пустая строка — файла нет или прочитать не удалось: отсутствие журнала
// не повод отказывать пользователю в отчёте.
//
// Общая реализация: тем же способом лаунчер показывает причину неудачного
// старта базы, и расходиться этим двум чтениям незачем.
func TailFile(path string, lines int, maxBytes int64) string {
	if path == "" || lines <= 0 {
		return ""
	}
	f, err := os.Open(path) //nolint:gosec // G304: путь собирает сама программа (журналы в профиле пользователя)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return ""
	}
	off := st.Size() - maxBytes
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return ""
	}
	text := strings.TrimRight(string(buf), "\r\n\t ")
	if off > 0 {
		// Первую строку обрезало серединой — отбрасываем её целиком.
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	out := strings.Split(text, "\n")
	if len(out) > lines {
		out = out[len(out)-lines:]
	}
	for i, ln := range out {
		out[i] = strings.TrimRight(ln, "\r")
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// StartupLogPath — путь к следу запусков, который пишет cmd/onebase/main.go.
// Пустая строка — домашний каталог недоступен.
func StartupLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".onebase", "startup.log")
}
