package ui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// GET -> real JS event/modal -> HTTP event/answer -> native form data -> submit.
func TestConcealQuestionHTTPNodeMatrix(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	for _, fresh := range []bool{false, true} {
		for _, style := range []string{"plain", "multiline", "readonly", "mask"} {
			for _, action := range []string{"dismiss", "answer", "normal", "edit", "clear", "revoke", "expired", "error"} {
				if style == "readonly" && (action == "edit" || action == "clear") {
					continue
				}
				t.Run(style+"/"+action+map[bool]string{true: "/new", false: "/existing"}[fresh], func(t *testing.T) {
					dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
						c, id := concealFixture(t, db)
						key, initial := id.String(), "12-34"
						if fresh {
							key = ""
							c.entity.Fields[len(c.entity.Fields)-1].Default = initial
						} else if err := db.Upsert(context.Background(), c.entity.Name, id, map[string]any{"Наименование": "образец", "Секрет": initial}, c.entity); err != nil {
							t.Fatal(err)
						}
						form := c.entity.Forms[0]
						form.Elements[0].Multiline = style == "multiline"
						form.Elements[0].ReadOnly = style == "readonly"
						if style == "mask" {
							form.Elements[0].InputMask = "00-00"
							form.Elements[0].Mask = "[0-9]{2}-[0-9]{2}"
						}
						form.ProgramAST = mustParse(t, concealTestModule+`
Процедура РаскрытьСВопросом()
 Если Объект.Причина = "проверена" Тогда
  Объект.Маской = Ложь;
  ПоказатьВопрос("Продолжить?", "ДаНет");
 КонецЕсли;
КонецПроцедуры
Процедура ПринятьОтвет()
 Сообщить("Ответ: " + ВопросОтвет);
КонецПроцедуры
`)
						form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementButton, Name: "РаскрытьСВопросом", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "РаскрытьСВопросом", metadata.FormEventOnAnswer: "ПринятьОтвет"}})
						now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
						c.server.concealClock = func() time.Time { return now }
						c = c.open(t, key)
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						cmd := exec.CommandContext(ctx, node, "static/conceal_question_http.js") //nolint:gosec // fixed test script; executable resolved by LookPath
						stdin, err := cmd.StdinPipe()
						if err != nil {
							t.Fatal(err)
						}
						stdout, err := cmd.StdoutPipe()
						if err != nil {
							t.Fatal(err)
						}
						var stderr bytes.Buffer
						cmd.Stderr = &stderr
						if err := cmd.Start(); err != nil {
							t.Fatal(err)
						}
						enc := json.NewEncoder(stdin)
						if err := enc.Encode(map[string]any{"id": key, "token": c.token, "initial": initial, "multiline": style == "multiline", "readonly": style == "readonly", "question": action != "normal", "action": action}); err != nil {
							t.Fatal(err)
						}
						scanner := bufio.NewScanner(stdout)
						var submitted map[string]string
						for scanner.Scan() {
							var message struct {
								Event, Submit map[string]string
								Calls         int
							}
							if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
								t.Fatalf("bridge: %s: %v", scanner.Text(), err)
							}
							if message.Submit != nil {
								submitted = message.Submit
								break
							}
							if action == "expired" && message.Event["_element"] == "" {
								now = now.Add(10 * time.Minute)
							}
							body := url.Values{}
							for k, v := range message.Event {
								body.Set(k, v)
							}
							rec := c.request(t, "EVENT", key, body.Get("_element"), body)
							if rec.Code != http.StatusOK {
								t.Fatalf("event: %d %s", rec.Code, rec.Body.String())
							}
							if _, err := stdin.Write(append(bytes.TrimSpace(rec.Body.Bytes()), '\n')); err != nil {
								t.Fatal(err)
							}
						}
						if err := cmd.Wait(); err != nil {
							t.Fatalf("node: %v\n%s", err, stderr.String())
						}
						if submitted == nil {
							t.Fatalf("no browser form data: %v %s", scanner.Err(), stderr.String())
						}
						body := url.Values{"Наименование": {uuid.NewString()}}
						for k, v := range submitted {
							body.Set(k, v)
						}
						rec := c.request(t, "POST", key, "", body)
						if rec.Code != http.StatusSeeOther {
							t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
						}
						if fresh {
							location := rec.Header().Get("Location")
							id, err = uuid.Parse(location[strings.LastIndex(location, "/")+1:])
							if err != nil {
								t.Fatal(err)
							}
						}
						row, err := db.GetByID(context.Background(), c.entity.Name, id, c.entity)
						if err != nil {
							t.Fatal(err)
						}
						want := initial
						if action == "edit" {
							want = "56-78"
						}
						if action == "clear" {
							want = ""
						}
						if got := anyString(valueCI(row, "Секрет")); got != want {
							t.Fatalf("persisted %q, want %q", got, want)
						}
					})
				})
			}
		}
	}
}
