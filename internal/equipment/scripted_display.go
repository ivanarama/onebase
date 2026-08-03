package equipment

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

func init() {
	Register("scripted_display", func() Device { return &scriptedDisplayDevice{width: 20} })
}

// scriptedDisplayDevice — декларативный драйвер дисплея покупателя: протокол
// задан ПАРАМЕТРАМИ из конфигурации, а не Go-кодом. В отличие от весов/оплаты
// (чистый текст), у дисплея команды бинарные и перемешаны с текстом, плюс
// кодировка кириллицы (обычно CP866). Поэтому шаблон строки описывается как
// «hex-команды + плейсхолдер {text}», а текст кодируется отдельно.
//
// Реализует CustomerDisplay, поэтому работает через существующий DSL-метод
// Показать и агентский /display без их изменения — как scripted/scripted_pay.
type scriptedDisplayDevice struct {
	conn    io.WriteCloser
	width   int
	initCmd []byte      // КомандаИниц (hex), напр. 1B40 = ESC @
	clear   []byte      // КомандаОчистки (hex), напр. 0C
	lines   [][2][]byte // для каждой строки: {hex-префикс, hex-суффикс} вокруг {text}
	encode  func(string) []byte
}

func (d *scriptedDisplayDevice) Kind() string { return "дисплей_покупателя" }

func (d *scriptedDisplayDevice) Connect(params map[string]string) error {
	if w := firstNonEmpty(params["ширина"], params["width"]); w != "" {
		if n, err := strconv.Atoi(w); err == nil && n > 0 {
			d.width = n
		}
	}

	var err error
	if d.initCmd, err = decodeHexParam(firstNonEmpty(params["командаиниц"], params["initcmd"])); err != nil {
		return fmt.Errorf("scripted_display: КомандаИниц: %w", err)
	}
	if d.clear, err = decodeHexParam(firstNonEmpty(params["командаочистки"], params["clearcmd"])); err != nil {
		return fmt.Errorf("scripted_display: КомандаОчистки: %w", err)
	}

	// Шаблоны строк ШаблонСтроки1, ШаблонСтроки2, … подряд, пока заданы.
	for i := 1; ; i++ {
		n := strconv.Itoa(i)
		tpl := firstNonEmpty(params["шаблонстроки"+n], params["linetpl"+n])
		if tpl == "" {
			break
		}
		prefix, suffix, err := parseLineTemplate(tpl)
		if err != nil {
			return fmt.Errorf("scripted_display: ШаблонСтроки%s: %w", n, err)
		}
		d.lines = append(d.lines, [2][]byte{prefix, suffix})
	}
	if len(d.lines) == 0 {
		return fmt.Errorf("scripted_display: не задан ни один ШаблонСтрокиN (напр. \"1B5141{text}0D\")")
	}

	// Для VFD по умолчанию CP866 (а не UTF-8, который даст кракозябры).
	d.encode = deviceEncoder(firstNonEmpty(params["кодировка"], params["encoding"], "cp866"))

	conn, err := openWriteTransport(params)
	if err != nil {
		return err
	}
	d.conn = conn
	return nil
}

func (d *scriptedDisplayDevice) Disconnect() error {
	if d.conn == nil {
		return nil
	}
	err := d.conn.Close()
	d.conn = nil
	return err
}

// ShowLines выводит строки по описанным шаблонам: КомандаИниц, КомандаОчистки,
// затем для каждой строки префикс + закодированный текст (обрезанный/дополненный
// до ширины) + суффикс. Недостающие строки выводятся пустыми, лишние — отбрасываются.
func (d *scriptedDisplayDevice) ShowLines(lines []string) error {
	var buf bytes.Buffer
	buf.Write(d.initCmd)
	buf.Write(d.clear)
	for i, tpl := range d.lines {
		text := ""
		if i < len(lines) {
			text = lines[i]
		}
		buf.Write(tpl[0])
		buf.Write(d.encode(d.fit(text)))
		buf.Write(tpl[1])
	}
	return d.write(buf.Bytes())
}

func (d *scriptedDisplayDevice) Clear() error {
	return d.write(append(append([]byte{}, d.initCmd...), d.clear...))
}

func (d *scriptedDisplayDevice) write(chunk []byte) error {
	if d.conn == nil {
		return fmt.Errorf("устройство не подключено")
	}
	_, err := d.conn.Write(chunk)
	return err
}

// fit обрезает или дополняет строку пробелами до ширины дисплея (по рунам,
// до кодирования — иначе кириллица в CP866 сломала бы подсчёт длины).
func (d *scriptedDisplayDevice) fit(s string) string {
	n := utf8.RuneCountInString(s)
	if n > d.width {
		return string([]rune(s)[:d.width])
	}
	return s + strings.Repeat(" ", d.width-n)
}

// decodeHexParam декодирует hex-строку команды (пробелы и двоеточия игнорируются).
// Пустая строка → nil без ошибки.
func decodeHexParam(h string) ([]byte, error) {
	h = strings.NewReplacer(" ", "", ":", "").Replace(h)
	if h == "" {
		return nil, nil
	}
	return hex.DecodeString(h)
}

// parseLineTemplate разбирает шаблон строки "1B5141{text}0D" на hex-префикс и
// hex-суффикс вокруг плейсхолдера {text}. Если {text} отсутствует, весь шаблон —
// префикс (текст допишется после него).
func parseLineTemplate(tpl string) (prefix, suffix []byte, err error) {
	const ph = "{text}"
	pre, post := tpl, ""
	if i := strings.Index(tpl, ph); i >= 0 {
		pre, post = tpl[:i], tpl[i+len(ph):]
	}
	if prefix, err = decodeHexParam(pre); err != nil {
		return nil, nil, fmt.Errorf("префикс: %w", err)
	}
	if suffix, err = decodeHexParam(post); err != nil {
		return nil, nil, fmt.Errorf("суффикс: %w", err)
	}
	return prefix, suffix, nil
}
