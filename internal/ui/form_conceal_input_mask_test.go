package ui

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// Inspect one actual control, rather than accepting attributes found elsewhere
// in the page (including another masked or concealed field).
func concealMaskInput(t *testing.T, page, name string) map[string]string {
	t.Helper()
	z := html.NewTokenizer(strings.NewReader(page))
	var found map[string]string
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				t.Fatal(z.Err())
			}
			if found == nil {
				t.Fatalf("input %q missing", name)
			}
			return found
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			if tok.Data != "input" {
				continue
			}
			attrs := make(map[string]string)
			for _, a := range tok.Attr {
				attrs[a.Key] = a.Val
			}
			if attrs["name"] == name {
				if found != nil {
					t.Fatalf("duplicate input %q", name)
				}
				found = attrs
			}
		}
	}
}

func TestConcealInputMaskHTTPMatrix(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		kind := "existing"
		if fresh {
			kind = "new-default"
		}
		for _, action := range []string{"hidden", "edit", "revoke", "expired", "readonly", "ordinary"} {
			t.Run(kind+"/"+action, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					// A public UUID may contain the old short secret "12-34".
					// Keep that collision in the HTTP fixture while using a distinct,
					// valid masked value for the strict whole-page disclosure check.
					const initial, edited = "1234-5678-9012-3456", "9876-5432-1098-7654"
					c, id := concealFixtureWithID(t, db, uuid.MustParse("00000012-3400-4000-8000-000000000001"))
					key := id.String()
					if fresh {
						key = ""
						for i := range c.entity.Fields {
							if c.entity.Fields[i].Name == "Секрет" {
								c.entity.Fields[i].Default = initial
							}
						}
					} else if err := db.Upsert(context.Background(), c.entity.Name, id, map[string]any{"Наименование": "обычное", "Секрет": initial}, c.entity); err != nil {
						t.Fatal(err)
					}
					el := c.entity.Forms[0].Elements[0]
					el.InputMask, el.Mask, el.Hint, el.AccessKey = "0000-0000-0000-0000", "[0-9]{4}-[0-9]{4}-[0-9]{4}-[0-9]{4}", "Введите код", "s"
					el.Required, el.ReadOnly = true, action == "readonly"
					el.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnChange: "Открыть"}
					if action == "ordinary" {
						el.Conceal, el.RevealWhen = false, ""
					}
					// The scalar form-attribute fallback is a separate template
					// branch. It keeps a mask without acquiring conceal semantics.
					c.entity.Forms[0].Attributes = append(c.entity.Forms[0].Attributes, &metadata.FormAttribute{Name: "КодФормы", TypeRef: "string"})
					c.entity.Forms[0].Elements = append(c.entity.Forms[0].Elements, &metadata.FormElement{
						Kind: metadata.FormElementField, Name: "КодФормы", DataPath: "КодФормы", InputMask: "XX-00",
					})
					now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
					c.server.concealClock = func() time.Time { return now }
					get := c.request(t, "GET", key, "", nil)
					if get.Code != http.StatusOK {
						t.Fatalf("GET: %d %s", get.Code, get.Body.String())
					}
					if !fresh && !strings.Contains(get.Body.String(), key) {
						t.Fatal("public collision UUID missing from HTTP response")
					}
					attrs := concealMaskInput(t, get.Body.String(), "Секрет")
					for k, want := range map[string]string{"type": "text", "autocomplete": "off", "data-ob-input-mask": "0000-0000-0000-0000", "placeholder": "____-____-____-____", "inputmode": "numeric", "pattern": el.Mask, "title": el.Hint, "accesskey": "s"} {
						if got, ok := attrs[k]; !ok || got != want {
							t.Fatalf("%s = %q (present=%v), want %q", k, got, ok, want)
						}
					}
					_, concealed := attrs["data-ob-concealed"]
					_, disabled := attrs["disabled"]
					_, readonly := attrs["readonly"]
					_, required := attrs["required"]
					if concealed != el.Conceal || disabled != el.Conceal || readonly != el.ReadOnly || required == el.ReadOnly {
						t.Fatalf("control state: %v", attrs)
					}
					if !el.ReadOnly && attrs["data-ob-fire-change"] != el.Name {
						t.Fatalf("change handler lost: %v", attrs)
					}
					wantValue := ""
					if !el.Conceal {
						wantValue = initial
					}
					if attrs["value"] != wantValue {
						t.Fatalf("GET did not preserve disclosure boundary: value=%q, want %q", attrs["value"], wantValue)
					}
					// Секрет «12-34» состоит из шестнадцатеричных цифр, и на
					// стыке групп случайного UUID страницы (id записи, сессии)
					// изредка складывается та же строка — проверка мигала примерно
					// раз на сотню прогонов. UUID секретом не являются: ищем
					// значение в странице без них.
					if el.Conceal && strings.Contains(concealUUIDPattern.ReplaceAllString(get.Body.String(), ""), initial) {
						t.Fatal("GET did not preserve disclosure boundary: concealed value present in page")
					}
					formAttrs := concealMaskInput(t, get.Body.String(), "КодФормы")
					if formAttrs["data-ob-input-mask"] != "XX-00" || formAttrs["placeholder"] != "__-__" {
						t.Fatalf("form attribute mask lost: %v", formAttrs)
					}
					for _, k := range []string{"disabled", "data-ob-concealed", "inputmode"} {
						if _, ok := formAttrs[k]; ok {
							t.Fatalf("ordinary form attribute acquired %s", k)
						}
					}
					if el.Conceal {
						// Extract the instance from this same GET, without opening
						// another form or touching the server grant ledger.
						match := regexp.MustCompile(`data-ob-conceal-context="([^"]+)"`).FindStringSubmatch(get.Body.String())
						if len(match) != 2 {
							t.Fatal("missing conceal instance")
						}
						c.token = match[1]
						assertConcealTTLResponse(t, c.event(t, key, "", nil), initial, false)
						assertConcealTTLResponse(t, c.event(t, key, "ПроверитьКонтроллер", url.Values{"Маской": {"false"}, "Секрет": {edited}}), initial, false)
						if action != "hidden" {
							assertConcealTTLResponse(t, c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}}), initial, true)
						}
						switch action {
						case "revoke":
							assertConcealTTLResponse(t, c.event(t, key, "Отозвать", nil), initial, false)
						case "expired":
							now = now.Add(10 * time.Minute)
							assertConcealTTLResponse(t, c.event(t, key, "", nil), initial, false)
						}
					} else {
						resp := c.event(t, key, "", url.Values{"Секрет": {initial}, "КодФормы": {"AB-12"}})
						if !resp.OK || anyString(valueCI(resp.Values, "Секрет")) != initial || anyString(valueCI(resp.Values, "КодФормы")) != "AB-12" {
							t.Fatalf("ordinary masked controls: %+v", resp)
						}
					}
					name := uuid.NewString()
					input := edited
					if el.ReadOnly {
						// A readonly control is included in FormData unchanged;
						// its editability is checked in HTML and the Node test.
						input = initial
					}
					post := c.request(t, "POST", key, "", url.Values{"Наименование": {name}, "Секрет": {input}, "Маской": {"false"}})
					if post.Code != http.StatusSeeOther {
						t.Fatalf("submit: %d %s", post.Code, post.Body.String())
					}
					if fresh {
						location := post.Header().Get("Location")
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
					want := initial
					if action == "edit" || action == "ordinary" {
						want = edited
					}
					if got := anyString(valueCI(row, "Секрет")); got != want || anyString(valueCI(row, "Наименование")) != name {
						t.Fatalf("persisted secret=%q name=%q, want secret=%q name=%q", got, row["Наименование"], want, name)
					}
				})
			})
		}
	}
}

// concealUUIDPattern — UUID в странице формы (записи, сессии, контекста).
var concealUUIDPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
