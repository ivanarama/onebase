package ui

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

func queryInputLimitVisibleText(t *testing.T, body string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.TextNode {
			text.WriteString(n.Data)
			text.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return text.String()
}

func TestTabularQueryInputLimitHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(method, func(t *testing.T) {
				for _, mode := range []string{"disabled", "exact-boundary", "below-boundary", "limit-hit", "module-must-not-run", "no-stale-viewer", "ordinary-warning", "ordinary-disabled"} {
					t.Run(mode, func(t *testing.T) {
						s, user, rep := tabularFixture(t, db)
						// Eight input rows, seven documents: the first document owns two rows.
						rep.Query = "SELECT 1 AS Ord, 'DOC-A' AS DocName UNION ALL SELECT 2, 'DOC-A' UNION ALL SELECT 3, 'DOC-B' UNION ALL SELECT 4, 'DOC-C' UNION ALL SELECT 5, 'DOC-D' UNION ALL SELECT 6, 'DOC-E' UNION ALL SELECT 7, 'DOC-F' UNION ALL SELECT 8, 'DOC-G' ORDER BY Ord"
						source := `Функция Build()
     Д=Новый ТабличныйДокумент;Н=1;Предыдущий="";
     Для Каждого Стр Из Результат Цикл
      Если Стр.DocName<>Предыдущий Тогда
       Д.Ячейка(Н,1).Текст=Стр.DocName;Н=Н+1;Предыдущий=Стр.DocName;
      КонецЕсли;
     КонецЦикла;Возврат Д;
    КонецФункции`
						if mode == "module-must-not-run" {
							source = `Функция Build() ВызватьИсключение "MODULE_MUST_NOT_RUN";КонецФункции`
						}
						s.reg.Load(runtime.LoadOptions{Reports: []*reportpkg.Report{rep}, Programs: map[string]*ast.Program{rep.Name: mustParse(t, source)}})
						limit := 3
						if mode == "disabled" || mode == "ordinary-disabled" {
							limit = 0
						}
						if mode == "exact-boundary" {
							limit = 8
						}
						if mode == "below-boundary" {
							limit = 9
						}
						path := "/ui/report/" + rep.Name
						oldPath := ""
						if mode == "no-stale-viewer" {
							full := tabularHTTP(s, user, "own-query-limit", method, path, nil)
							if full.Code != http.StatusSeeOther {
								t.Fatalf("prior full result HTTP%d: %s", full.Code, full.Body.String())
							}
							oldPath = full.Header().Get("Location")
						}
						if strings.HasPrefix(mode, "ordinary-") {
							rep.ResultProc = ""
						}
						s.cfg.Limits.ReportMaxRows = limit
						before := len(s.tabularResults.items)
						response := tabularHTTP(s, user, "own-query-limit", method, path, nil)
						if s.cfg.Limits.ReportMaxRows != limit {
							t.Fatal("query budget was changed")
						}
						if strings.HasPrefix(mode, "ordinary-") {
							if response.Code != http.StatusOK {
								t.Fatalf("ordinary policy HTTP%d: %s", response.Code, response.Body.String())
							}
							// Inspect document text, not a translated string in inert scripts.
							visible := queryInputLimitVisibleText(t, response.Body.String())
							warning := strings.Contains(visible, "report_max_rows")
							if warning != (limit > 0) {
								t.Fatal("ordinary query warning policy changed")
							}
							if len(s.tabularResults.items) != 0 {
								t.Fatal("ordinary query created tabular snapshot")
							}
							if strings.Contains(visible, "DOC-G") != (limit == 0) {
								t.Fatal("ordinary row-limit policy changed")
							}
							return
						}
						rejected := mode == "limit-hit" || mode == "module-must-not-run" || mode == "no-stale-viewer"
						if rejected {
							if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "report_max_rows") {
								t.Fatalf("cropped input was not refused before module: HTTP%d %s", response.Code, response.Body.String())
							}
							if response.Header().Get("Location") != "" || len(s.tabularResults.items) != before {
								t.Fatal("refusal published or reused a viewer")
							}
							if oldPath != "" && strings.Contains(response.Body.String(), oldPath) {
								t.Fatal("old viewer presented as new result")
							}
							return
						}
						if response.Code != http.StatusSeeOther {
							t.Fatalf("complete input refused: HTTP%d %s", response.Code, response.Body.String())
						}
						viewer := response.Header().Get("Location")
						snapshot := s.tabularResults.items[strings.TrimPrefix(viewer, "/ui/tabular/")]
						if snapshot == nil || len(snapshot.doc.Cells) != 7 {
							t.Fatal("complete input lost documents")
						}
						view := tabularHTTP(s, user, "own-query-limit", http.MethodGet, viewer, nil)
						if view.Code != http.StatusOK || !strings.Contains(view.Body.String(), "DOC-G") {
							t.Fatal("full viewer missing last document")
						}
					})
				}
			})
		}
	})
}
