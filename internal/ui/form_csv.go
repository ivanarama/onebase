package ui

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

type listProcessorLink struct {
	Title string
	URL   string
}

func (s *Server) listProcessorLinks(r *http.Request, entity *metadata.Entity, lang string) []listProcessorLink {
	form := pickManagedForm(entity, "list")
	if form == nil {
		return nil
	}
	var links []listProcessorLink
	for _, link := range form.ListProcessors {
		proc := s.reg.GetProcessor(link.Name)
		if proc == nil || !s.can(r, "processor", proc.Name, "run") || !s.canRunExternalProc(r, proc) {
			continue
		}
		title := link.Title[lang]
		if title == "" {
			title = link.Title["ru"]
		}
		if title == "" {
			title = proc.Title
		}
		links = append(links, listProcessorLink{Title: title, URL: "/ui/processor/" + url.PathEscape(proc.Name)})
	}
	return links
}

type formCSVDownload struct {
	Name    string `json:"name"`
	Content []byte `json:"content"`
}

const maxFormCSVBytes = 8 << 20

func newFormCSVDownloadBuiltin(sink **formCSVDownload) interpreter.BuiltinFunc {
	return func(args []any, _ string, _ int) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("СкачатьCSV(ИмяФайла, Строки): ожидаются два аргумента")
		}
		name, ok := args[0].(string)
		if !ok || strings.TrimSpace(name) != name || len(name) > 200 ||
			!strings.HasSuffix(strings.ToLower(name), ".csv") || len(name) <= len(".csv") ||
			strings.ContainsAny(name, `/\:<>"|?*`) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("СкачатьCSV: укажите имя файла с расширением .csv")
		}
		table, ok := args[1].(*interpreter.Array)
		if !ok || len(table.Iterate()) == 0 {
			return nil, fmt.Errorf("СкачатьCSV: ожидаются заголовки и строки таблицы")
		}
		var buf bytes.Buffer
		buf.WriteString("\uFEFF")
		writer := csv.NewWriter(&buf)
		writer.Comma = ';'
		writer.UseCRLF = true
		width := 0
		for i, row := range table.Iterate() {
			line, ok := row.(*interpreter.Array)
			if !ok || len(line.Iterate()) == 0 || i > 0 && len(line.Iterate()) != width {
				return nil, fmt.Errorf("СкачатьCSV: недопустимый состав строки %d", i+1)
			}
			width = len(line.Iterate())
			cells := make([]string, width)
			rowBytes := 0
			for j, cell := range line.Iterate() {
				cells[j] = formCSVCell(cell)
				rowBytes += len(cells[j])
			}
			if rowBytes > maxFormCSVBytes || buf.Len()+rowBytes > maxFormCSVBytes {
				return nil, fmt.Errorf("СкачатьCSV: файл больше 8 МиБ. Сократите период выгрузки")
			}
			if err := writer.Write(cells); err != nil {
				return nil, err
			}
			writer.Flush()
			if buf.Len() > maxFormCSVBytes {
				return nil, fmt.Errorf("СкачатьCSV: файл больше 8 МиБ. Сократите период выгрузки")
			}
		}
		if err := writer.Error(); err != nil {
			return nil, err
		}
		*sink = &formCSVDownload{Name: name, Content: buf.Bytes()}
		return nil, nil
	}
}

func formCSVCell(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case time.Time:
		return v.Format("02.01.2006 15:04:05")
	case bool:
		if v {
			return "Да"
		}
		return "Нет"
	case string:
		trimmed := strings.TrimLeftFunc(v, unicode.IsSpace)
		if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
			return "'" + v
		}
		return v
	default:
		return fmt.Sprint(v)
	}
}
