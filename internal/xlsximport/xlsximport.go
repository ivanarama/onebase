// Package xlsximport собирает черновик макета печатной формы из бланка .xlsx
// (план 155, обсуждение #1109).
//
// Зачем: теги в декларативном макете были всегда ({{Номер}}, {{Дата | date}},
// {{Итог.Товары.Сумма | number:2}} — см. internal/printform/binding.go), не
// хватало удобного редактора бланка. Расставить объединения, ширины колонок,
// границы и поля листа в Excel дешевле, чем в YAML, поэтому Excel и берётся
// редактором: пользователь рисует бланк, пишет в ячейки те же теги, импорт
// превращает лист в printforms/<имя>.layout.yaml — дальше это обычный макет,
// и редактор, предпросмотр, HTML, PDF и внешние печатные формы работают даром.
//
// Что НЕ переносится, попадает в Result.Warnings, а не теряется молча: формулы
// (берётся вычисленное значение), условное форматирование, повороты текста,
// диагональные границы, второй и далее листы. Обещать «в PDF будет как в Excel»
// нельзя: PDF рисует собственный рендерер internal/sheet, воспроизводится то
// подмножество оформления, которое есть у sheet.Cell.
//
// Недоверенный ввод: лимит размера файла, лимит распаковки zip (защита от
// zip-бомбы) и потолки строк/колонок; вся работа с excelize — под recover,
// как в соседнем internal/pdfimport.
package xlsximport

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ivantit66/onebase/internal/printform"
)

const (
	// MaxFileSize — лимит размера .xlsx (5 МБ). Бланк со сканом внутри в этот
	// размер не влезет, и это правильно: картинку-подложку импорт всё равно не
	// перенесёт осмысленно.
	MaxFileSize = 5 << 20

	// MaxRows/MaxCols — потолок разбираемой области листа. Excel объявляет
	// «использованный диапазон» щедро (после чистки строк он остаётся раздутым),
	// и без потолка импорт распухшего листа съел бы память на пустых ячейках.
	MaxRows = 500
	MaxCols = 64

	// unzipLimit — потолок распаковки для excelize (защита от zip-бомбы).
	unzipLimit = 64 << 20
)

var (
	// ErrFileTooLarge — файл больше MaxFileSize.
	ErrFileTooLarge = fmt.Errorf("файл больше %d МБ — слишком большой для импорта", MaxFileSize>>20)
	// ErrParse — файл не открылся как книга Excel (битый, чужой формат, пароль).
	ErrParse = errors.New("не удалось прочитать файл Excel (возможно, он повреждён, защищён паролем или это не .xlsx)")
	// ErrSheetNotFound — запрошенного листа нет в книге.
	ErrSheetNotFound = errors.New("лист не найден в книге")
	// ErrEmptySheet — на листе нет ни текста, ни оформления: импортировать нечего.
	ErrEmptySheet = errors.New("лист пуст — нечего импортировать")
)

// Options управляет импортом.
type Options struct {
	// Sheet — имя листа. Пусто — первый лист книги.
	Sheet string
	// TableParts — имена табличных частей документа, к которому привязывается
	// форма. Нужны, чтобы отличить колонку ТЧ ({{Товары.Количество}}) от поля
	// по ссылке ({{Склад.Наименование}}): по написанию они неразличимы, и
	// угадывать здесь нельзя. Пустой список — импорт не разворачивает repeat
	// вовсе и делает плоский макет.
	TableParts []string
}

// Result — результат импорта: черновик макета и список того, что не перенесено.
type Result struct {
	Layout   *printform.LayoutTemplate
	Warnings []string
}

// ImportBytes собирает черновик макета из содержимого .xlsx.
//
// Name/Document у макета не заполняются — их проставляет вызывающий (имя формы
// и привязку к документу знает конфигуратор/CLI, а не бланк).
func ImportBytes(data []byte, opts Options) (res *Result, err error) {
	if len(data) > MaxFileSize {
		return nil, ErrFileTooLarge
	}
	if len(data) == 0 {
		return nil, ErrParse
	}

	// excelize на битом вводе обычно возвращает ошибку, но разбор чужого zip —
	// не то место, где стоит полагаться на «обычно»: паника здесь уронила бы
	// весь конфигуратор.
	defer func() {
		if rec := recover(); rec != nil {
			res, err = nil, fmt.Errorf("%w: %v", ErrParse, rec)
		}
	}()

	f, oerr := excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		UnzipSizeLimit:    unzipLimit,
		UnzipXMLSizeLimit: unzipLimit,
	})
	if oerr != nil {
		return nil, fmt.Errorf("%w: %v", ErrParse, oerr)
	}
	defer closeQuietly(f)

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, ErrParse
	}

	var w warnings
	name, serr := resolveSheet(sheets, opts.Sheet)
	if serr != nil {
		return nil, serr
	}
	if len(sheets) > 1 {
		w.addf("Импортирован лист «%s»; остальные листы книги (%d) не переносятся.", name, len(sheets)-1)
	}

	g, gerr := readGrid(f, name, &w)
	if gerr != nil {
		return nil, gerr
	}

	lt := buildLayout(g, opts.TableParts, &w)
	return &Result{Layout: lt, Warnings: w.list()}, nil
}

// resolveSheet выбирает лист: заданный по имени (без учёта регистра) либо первый.
func resolveSheet(sheets []string, want string) (string, error) {
	want = strings.TrimSpace(want)
	if want == "" {
		return sheets[0], nil
	}
	for _, s := range sheets {
		if strings.EqualFold(s, want) {
			return s, nil
		}
	}
	return "", fmt.Errorf("%w: «%s» (в книге есть: %s)", ErrSheetNotFound, want, strings.Join(sheets, ", "))
}

func closeQuietly(f *excelize.File) {
	_ = f.Close()
}

// warnings копит сообщения о непереносимом, отбрасывая повторы: одно и то же
// «формулы не переносятся» на сорока ячейках — сорок одинаковых строк в баннере,
// после которых пользователь перестаёт читать баннер вообще.
type warnings struct {
	seen  map[string]bool
	items []string
}

func (w *warnings) addf(format string, args ...any) {
	w.add(fmt.Sprintf(format, args...))
}

func (w *warnings) add(msg string) {
	if w.seen == nil {
		w.seen = make(map[string]bool)
	}
	if w.seen[msg] {
		return
	}
	w.seen[msg] = true
	w.items = append(w.items, msg)
}

func (w *warnings) list() []string { return w.items }
