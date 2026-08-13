package configcheck

// INT-02 / issue #786: advisory-предупреждение о пустом auth у изменяющего
// сервиса. Пустой auth молча нормализуется к none — предупреждаем, но не
// роняем check; явный auth: none валиден и предупреждения не вызывает.

import (
	"testing"

	"github.com/ivantit66/onebase/internal/httpservice"
	"github.com/ivantit66/onebase/internal/project"
)

func TestCheckHTTPServiceAuthWarnings(t *testing.T) {
	mut := func(name, auth string, rateLimit int) *httpservice.Service {
		s := &httpservice.Service{
			Name: name, RootURL: name, Auth: auth, RateLimit: rateLimit,
			Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"POST": "H"}}},
		}
		s.Normalize()
		return s
	}
	readOnly := &httpservice.Service{
		Name: "ЧтениеБезAuth", RootURL: "ro",
		Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"GET": "H"}}},
	}
	readOnly.Normalize()

	proj := &project.Project{HTTPServices: []*httpservice.Service{
		mut("ПустойAuthИзменяющий", "", 0), // пустой auth + POST + без rate_limit → предупреждение
		mut("ЯвныйNone", "none", 0),        // явный none → без предупреждения
		mut("ПустойСРейтЛимитом", "", 60),  // пустой auth, но есть rate_limit → без предупреждения
		readOnly, // пустой auth, но только GET → без предупреждения
	}}

	warnings := CheckHTTPServiceAuthWarnings(proj)
	if len(warnings) != 1 {
		t.Fatalf("ожидалось ровно 1 предупреждение, получено %d: %+v", len(warnings), warnings)
	}
	if warnings[0].Object != "ПустойAuthИзменяющий" {
		t.Fatalf("предупреждение не про тот сервис: %q", warnings[0].Object)
	}
}
