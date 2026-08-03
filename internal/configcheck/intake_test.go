package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/httpservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

func parseIntakeModule(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "m.module.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

func TestCheckIntakes(t *testing.T) {
	good := parseIntakeModule(t, "Функция Обработать(К) Экспорт Возврат 1; КонецФункции")
	noproc := parseIntakeModule(t, "Функция Иное(К) Экспорт Возврат 1; КонецФункции")

	mk := func(name, endpoint, handler string) *metadata.Intake {
		in := &metadata.Intake{Name: name, Transport: "http", Endpoint: endpoint, Handler: handler,
			Idempotency: metadata.IntakeIdempotency{Key: "event_id"}}
		in.Normalize()
		return in
	}
	svc := &httpservice.Service{Name: "Сайт", RootURL: "site"}
	svc.Normalize()

	proj := &project.Project{
		Modules:      map[string]*ast.Program{"Good": good, "NoProc": noproc},
		HTTPServices: []*httpservice.Service{svc},
		Intakes: []*metadata.Intake{
			mk("A", "/hs/intake/a", "Good"),    // корректный — без замечаний
			mk("B", "/site/b", "Good"),         // endpoint не начинается с /hs/
			mk("C", "/hs/site/c", "Good"),      // коллизия с корнем сервиса «Сайт»
			mk("D", "/hs/intake/d", "Missing"), // нет модуля обработчика
			mk("E", "/hs/intake/e", "NoProc"),  // в модуле нет процедуры Обработать
		},
	}
	issues := CheckIntakes(proj)

	flagged := map[string]bool{}
	joined := ""
	for _, is := range issues {
		flagged[is.Object] = true
		joined += is.Object + ": " + is.Message + "\n"
		if is.Object == "A" {
			t.Errorf("для корректного шлюза A не должно быть замечаний: %s", is.Message)
		}
	}
	for _, obj := range []string{"B", "C", "D", "E"} {
		if !flagged[obj] {
			t.Errorf("ожидалось замечание для шлюза %s\nвсе замечания:\n%s", obj, joined)
		}
	}
	if !strings.Contains(joined, "затенит сервис") {
		t.Errorf("нет предупреждения о затенении сервиса:\n%s", joined)
	}
	if !strings.Contains(joined, "/hs/") {
		t.Errorf("нет предупреждения о префиксе /hs/:\n%s", joined)
	}
}
