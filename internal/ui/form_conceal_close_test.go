package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestConcealCancelledClose(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, granted := range []bool{false, true} {
			for _, mode := range []string{"discard", "save"} {
				for _, reauthorize := range []bool{false, true} {
					t.Run(fmt.Sprintf("existing=%t/granted=%t/%s/reauthorize=%t", existing, granted, mode, reauthorize), func(t *testing.T) {
						dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
							c, id := concealFixture(t, db)
							form := c.entity.Forms[0]
							form.ProgramAST = mustParse(t, concealTestModule+`
Процедура ПроверитьЗакрытие(Отказ)
 Объект.Маской = Ложь;
 Отказ = Истина;
КонецПроцедуры
`)
							form.Handlers[metadata.FormEventBeforeClose] = "ПроверитьЗакрытие"
							const initialDefault = "серверное умолчание"
							for i := range c.entity.Fields {
								if c.entity.Fields[i].Name == "Секрет" {
									c.entity.Fields[i].Default = initialDefault
								}
							}
							key, initial := "", initialDefault
							if existing {
								key, initial = id.String(), "секрет"
							}
							c = c.open(t, key)
							if granted {
								shown := c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}})
								if !shown.OK || len(shown.Revealed) != 1 || anyString(valueCI(shown.Values, "Секрет")) != initial {
									t.Fatalf("initial grant failed: %+v", shown)
								}
							}
							body := closeIntentBody(uuid.NewString(), "close", "обычное")
							body.Set("_close_mode", mode)
							body.Set("_id", key)
							// Save mode may persist before BeforeClose, so submit the
							// canonical value when the browser already has a grant.
							body.Set("Секрет", "подмена")
							if granted {
								body.Set("Секрет", initial)
							}
							ensureEntityCloseIntentSchema(c.entity, body)
							closeRec := c.request(t, "CLOSE", key, "", body)
							closed := decodeCloseIntentResponse(t, closeRec)
							if closeRec.Code != http.StatusOK || !closed.OK || closed.Close == nil || closed.Close.Allowed || closed.Close.Saved != (mode == "save") {
								t.Fatalf("cancelled close: status=%d response=%+v", closeRec.Code, closed)
							}
							assertHidden := func(resp formEventResponse) {
								t.Helper()
								if !resp.OK || len(resp.Revealed) != 0 || anyString(valueCI(resp.Values, "Секрет")) != "" {
									t.Fatalf("cancelled close granted disclosure: %+v", resp)
								}
							}
							assertHidden(closed)
							assertHidden(c.event(t, key, "", url.Values{"Маской": {"false"}}))
							if !reauthorize {
								// The replay ledger returns a redacted recovery tombstone;
								// it must not reissue authority. Test fresh authorization
								// separately, before this recovery invalidates the context.
								replayed := c.request(t, "CLOSE", key, "", body)
								replay := decodeCloseIntentResponse(t, replayed)
								wantStatus := http.StatusConflict
								if mode == "save" {
									wantStatus = http.StatusOK
								}
								if replayed.Code != wantStatus || replay.Close == nil || replay.Close.IntentID != closed.Close.IntentID || replay.Close.Allowed || replay.Close.Saved != closed.Close.Saved || replay.SavedID != closed.SavedID || replay.Version != closed.Version || len(replay.Revealed) != 0 || anyString(valueCI(replay.Values, "Секрет")) != "" {
									t.Fatalf("unsafe close replay: %d %+v", replayed.Code, replay)
								}
								assertHidden(c.event(t, key, "", nil))
							}
							if !existing && mode == "save" {
								// The unsaved identity is consumed by the durable save.
								var err error
								id, err = uuid.Parse(closed.SavedID)
								if err != nil {
									t.Fatal(err)
								}
								key = id.String()
								c = c.open(t, key)
							}
							want := initial
							input := "подмена после отказа"
							if reauthorize {
								shown := c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}})
								if !shown.OK || len(shown.Revealed) != 1 || anyString(valueCI(shown.Values, "Секрет")) != initial {
									t.Fatalf("fresh trusted grant failed: %+v", shown)
								}
								want, input = "разрешённая правка", "разрешённая правка"
							}
							rec := c.request(t, "POST", key, "", url.Values{"Наименование": {"обычная правка"}, "Секрет": {input}, "Маской": {"false"}})
							if rec.Code != http.StatusSeeOther {
								t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
							}
							if key == "" {
								location := rec.Header().Get("Location")
								var err error
								id, err = uuid.Parse(location[strings.LastIndex(location, "/")+1:])
								if err != nil {
									t.Fatal(err)
								}
							}
							row, err := db.GetByID(context.Background(), c.entity.Name, id, c.entity)
							if err != nil {
								t.Fatal(err)
							}
							if row["Секрет"] != want || row["Наименование"] != "обычная правка" {
								t.Fatalf("submit lost conceal or ordinary fields: %v", row)
							}
						})
					})
				}
			}
		}
	}
}
