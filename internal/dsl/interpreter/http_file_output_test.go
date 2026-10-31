package interpreter_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// audioBody проверяет побайтовое сохранение файла с нулевыми байтами
// и последовательностями, невалидными в UTF-8.
var audioBody = append([]byte("OggS\x00\x02"), bytes.Repeat([]byte{0x00, 0xff, 0x80, 0x0d, 0x0a}, 2000)...)

type methodLog struct {
	mu   sync.Mutex
	list []string
}

func (l *methodLog) add(m string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = append(l.list, m)
}

func (l *methodLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.list...)
}

func audioServer(t *testing.T, hits *int32, methods *methodLog) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if methods != nil {
			methods.add(r.Method)
		}
		switch r.URL.Path {
		case "/rec.ogg":
			w.Header().Set("Content-Type", "audio/ogg")
			_, _ = w.Write(audioBody)
		case "/broken":
			// Объявлено больше, чем отдано: клиент получит обрыв тела.
			w.Header().Set("Content-Length", strconv.Itoa(len(audioBody)*2))
			_, _ = w.Write(audioBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func hostPort(t *testing.T, srv *httptest.Server) (string, float64) {
	t.Helper()
	addr := strings.TrimPrefix(srv.URL, "http://")
	host, portStr, ok := strings.Cut(addr, ":")
	require.True(t, ok)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return host, float64(port)
}

func dirLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

const getToFileSrc = `
Функция Тест()
	Соединение = Новый HTTPСоединение(Хост, Порт);
	Запрос = Новый HTTPЗапрос(Ресурс);
	Ответ = Соединение.Получить(Запрос, Путь);
	Возврат Строка(Ответ.КодСостояния) + "|" + Ответ.ПолучитьЗаголовок("Content-Type") + "|"
		+ ?(Ответ.ПолучитьТелоКакСтроку() = Неопределено, "нет строки", "строка") + "|"
		+ ?(Ответ.ПолучитьИмяФайлаТела() = Путь, "имя файла", "другое имя");
КонецФункции
`

func TestHTTPConnection_GetWritesBinaryBodyToFile(t *testing.T) {
	var hits int32
	srv := audioServer(t, &hits, nil)
	host, port := hostPort(t, srv)
	dir := t.TempDir()
	path := filepath.Join(dir, "rec.ogg")

	vars := interpreter.NewHTTPFunctions(nil)
	vars["Хост"], vars["Порт"], vars["Ресурс"], vars["Путь"] = host, port, "/rec.ogg", path
	result := runHTTPSrc(t, getToFileSrc, vars)

	assert.Equal(t, "200|audio/ogg|нет строки|имя файла", result)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(audioBody, got), "тело в файле обязано совпасть побайтно: %d из %d байт", len(got), len(audioBody))
	assert.Equal(t, []string{"rec.ogg"}, dirLeftovers(t, dir), "временный файл не должен оставаться")
}

func TestHTTPConnection_SendForMethodWritesBodyToFile(t *testing.T) {
	var hits int32
	var methods methodLog
	srv := audioServer(t, &hits, &methods)
	host, port := hostPort(t, srv)
	path := filepath.Join(t.TempDir(), "rec.ogg")

	src := `
Функция Тест()
	Соединение = Новый HTTPСоединение(Хост, Порт);
	Ответ = Соединение.ОтправитьДля(Новый HTTPЗапрос("/rec.ogg"), "GET", Путь);
	Возврат Ответ.КодСостояния;
КонецФункции
`
	vars := interpreter.NewHTTPFunctions(nil)
	vars["Хост"], vars["Порт"], vars["Путь"] = host, port, path
	result := runHTTPSrc(t, src, vars)

	assert.Equal(t, float64(200), result)
	assert.Equal(t, []string{"GET"}, methods.all())
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(audioBody, got))
}

func TestHTTPConnection_FileOutputFailureLeavesNoPartialFile(t *testing.T) {
	cases := []struct {
		name     string
		resource string
		maxBytes int64
		wantErr  string
	}{
		{"лимит", "/rec.ogg", 100, "превышает лимит 100 байт"},
		{"обрыв тела", "/broken", 0, "ошибка чтения ответа"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits int32
			srv := audioServer(t, &hits, nil)
			host, port := hostPort(t, srv)
			dir := t.TempDir()
			path := filepath.Join(dir, "rec.ogg")
			// Прежнее содержимое по пути не должно смениться обрезком.
			require.NoError(t, os.WriteFile(path, []byte("прежний файл"), 0o600))

			vars := interpreter.NewHTTPFunctionsWith(nil, interpreter.HTTPOptions{MaxFileBytes: tc.maxBytes})
			vars["Хост"], vars["Порт"], vars["Ресурс"], vars["Путь"] = host, port, tc.resource, path
			err := runHTTPSrcError(t, getToFileSrc, vars)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			got, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			assert.Equal(t, "прежний файл", string(got))
			assert.Equal(t, []string{"rec.ogg"}, dirLeftovers(t, dir), "частичный файл обязан быть удалён")
		})
	}
}

func TestHTTPConnection_FileOutputDeniedBySandboxBeforeRequest(t *testing.T) {
	var hits int32
	srv := audioServer(t, &hits, nil)
	host, port := hostPort(t, srv)
	dir := t.TempDir()
	path := filepath.Join(dir, "rec.ogg")

	run := func(src string) (any, error) {
		// Хост даёт свою фабрику с сетевым guard'ом; песочница запрещает
		// только файлы — сеть остаётся, запись в файл закрывается.
		vars := interpreter.NewHTTPFunctions(nil)
		vars["Хост"], vars["Порт"], vars["Ресурс"], vars["Путь"] = host, port, "/rec.ogg", path
		var result any
		err := interpreter.New().RunSandboxed(parseProc(t, src), nil, interpreter.SandboxProfile{DenyFile: true}, &result, vars)
		return result, err
	}

	_, err := run(getToFileSrc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "файловые операции запрещены")
	assert.Equal(t, int32(0), atomic.LoadInt32(&hits), "запрет обязан сработать до сетевого запроса")
	assert.Empty(t, dirLeftovers(t, dir))

	// Строковое чтение ответа (без файла) песочница с запретом файлов не трогает.
	result, err := run(`
Функция Тест()
	Соединение = Новый HTTPСоединение(Хост, Порт);
	Ответ = Соединение.Получить(Новый HTTPЗапрос(Ресурс));
	Возврат Ответ.КодСостояния;
КонецФункции
`)
	require.NoError(t, err)
	assert.Equal(t, float64(200), result)
}
