package interpreter_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Пошаговый замер из модуля, как в 1С: разность двух
// ТекущаяУниверсальнаяДатаВМиллисекундах — миллисекунды, а
// ЗаписьЖурналаРегистрации с пропущенными аргументами пишет строку в журнал
// сервера: component=dsl, event, comment, уровень по УровеньЖурналаРегистрации.
func TestEventLogAndMillis(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	before := time.Now().UnixMilli()
	got := runHTTPSrc(t, `Функция Тест()
  Начало = ТекущаяУниверсальнаяДатаВМиллисекундах();
  ЗаписьЖурналаРегистрации("Замер кнопки", УровеньЖурналаРегистрации.Предупреждение, , , "шаг 1: " + (ТекущаяУниверсальнаяДатаВМиллисекундах() - Начало) + " мс");
  ЗаписьЖурналаРегистрации("Замер кнопки", , , , "по умолчанию");
  WriteLogEvent("Measure", EventLogLevel.Error, , , "english");
  Возврат Начало;
КонецФункции`, nil)
	after := time.Now().UnixMilli()

	ms, ok := got.(float64)
	require.True(t, ok, "миллисекунды — число, получено %T", got)
	assert.GreaterOrEqual(t, int64(ms), before)
	assert.LessOrEqual(t, int64(ms), after)

	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var r map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &r))
		if r["component"] == "dsl" {
			records = append(records, r)
		}
	}
	require.Len(t, records, 3)
	assert.Equal(t, "WARN", records[0]["level"])
	assert.Equal(t, "Замер кнопки", records[0]["event"])
	assert.Regexp(t, `^шаг 1: \d+ мс$`, records[0]["comment"])
	assert.NotEmpty(t, records[0]["source"])
	assert.Equal(t, "INFO", records[1]["level"], "пропущенный уровень — Информация")
	assert.Equal(t, "по умолчанию", records[1]["comment"])
	assert.Equal(t, "ERROR", records[2]["level"])
	assert.Equal(t, "Measure", records[2]["event"])
}

// Длинный комментарий не раздувает журнал сервера.
func TestEventLogTruncatesLongComment(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	runHTTPSrc(t, `Процедура Тест()
  Т = "";
  Для Н = 1 По 5000 Цикл
    Т = Т + "ж";
  КонецЦикла;
  ЗаписьЖурналаРегистрации("Длинный", , , , Т);
КонецПроцедуры`, nil)
	var r map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &r))
	comment, _ := r["comment"].(string)
	assert.Equal(t, 4001, len([]rune(comment)), "4000 символов и многоточие")
	assert.True(t, strings.HasSuffix(comment, "…"))
}

// Перевод строки в комментарии из модуля не даёт второй строки журнала.
func TestEventLogStripsControlChars(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	runHTTPSrc(t, `Процедура Тест()
  ЗаписьЖурналаРегистрации("Замер", , , , "шаг" + Символы.ПС + "level=ERROR msg=поддельная");
КонецПроцедуры`, nil)
	out := strings.TrimRight(buf.String(), "\n")
	assert.NotContains(t, out, "\n", "комментарий с переводом строки разбил запись журнала")
	assert.Contains(t, out, "шаг level=ERROR")
}
