package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestConcealNewDefaultRevealAndSave(t *testing.T) {
	const initial = "серверное умолчание"
	for _, control := range []string{"plain", "multiline", "readonly"} {
		t.Run(control, func(t *testing.T) {
			for _, action := range []string{"hidden", "reveal", "edit", "clear", "server-write"} {
				if control == "readonly" && (action == "edit" || action == "clear") {
					continue
				}
				t.Run(action, func(t *testing.T) {
					dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
						c, _ := concealFixture(t, db)
						for i := range c.entity.Fields {
							if c.entity.Fields[i].Name == "Секрет" {
								c.entity.Fields[i].Default = initial
							}
						}
						el := c.entity.Forms[0].Elements[0]
						el.Multiline = control == "multiline"
						el.ReadOnly = control == "readonly"
						c = c.open(t, "")
						hidden := c.event(t, "", "", url.Values{"Секрет": {"подмена"}, "Маской": {"false"}})
						if !hidden.OK || len(hidden.Revealed) != 0 || anyString(valueCI(hidden.Values, "Секрет")) != "" {
							t.Fatalf("default disclosed without grant: %+v", hidden)
						}
						body := url.Values{"Наименование": {uuid.NewString()}, "Секрет": {"подмена"}}
						want := initial
						var savedID uuid.UUID
						if action == "server-write" {
							saved := c.event(t, "", "ЗаписатьСервером", body)
							if !saved.OK || saved.SavedID == "" {
								t.Fatalf("server write: %+v", saved)
							}
							var err error
							savedID, err = uuid.Parse(saved.SavedID)
							if err != nil {
								t.Fatal(err)
							}
							want = "сервер"
						} else {
							if action != "hidden" {
								shown := c.event(t, "", "Показать", url.Values{"Причина": {"проверена"}, "Секрет": {"подмена"}})
								if !shown.OK || len(shown.Revealed) != 1 || anyString(valueCI(shown.Values, "Секрет")) != initial {
									t.Fatalf("reveal lost server default: %+v", shown)
								}
								// Submit exactly the disclosed value, as the browser does
								// when the user leaves the newly enabled control unchanged.
								body.Set("Секрет", anyString(valueCI(shown.Values, "Секрет")))
								if action == "edit" {
									want = "изменено"
									body.Set("Секрет", want)
								} else if action == "clear" {
									want = ""
									body.Set("Секрет", want)
								}
							}
							rec := c.request(t, "POST", "", "", body)
							if rec.Code != http.StatusSeeOther {
								t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
							}
							location := rec.Header().Get("Location")
							var err error
							savedID, err = uuid.Parse(location[strings.LastIndex(location, "/")+1:])
							if err != nil {
								t.Fatal(err)
							}
						}
						row, err := db.GetByID(context.Background(), c.entity.Name, savedID, c.entity)
						if err != nil {
							t.Fatal(err)
						}
						if got := anyString(valueCI(row, "Секрет")); got != want {
							t.Fatalf("saved secret = %q, want %q", got, want)
						}
					})
				})
			}
		})
	}
}
