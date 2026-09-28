package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestDocumentListBasedOnActionsRespectReceiverWritePermission(t *testing.T) {
	source := &metadata.Entity{
		Name:  "ЗаказПокупателя",
		Title: "Заказ покупателя",
		Kind:  metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
	}
	allowed := &metadata.Entity{
		Name:    "СчетПокупателю",
		Title:   "Счёт покупателю",
		Kind:    metadata.KindDocument,
		BasedOn: []string{source.Name},
	}
	denied := &metadata.Entity{
		Name:    "ЗакрытыйДокумент",
		Title:   "Закрытый документ",
		Kind:    metadata.KindDocument,
		BasedOn: []string{source.Name},
	}
	treeSource := &metadata.Entity{
		Name:         "Проекты",
		Title:        "Проекты",
		Kind:         metadata.KindCatalog,
		Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	treeReceiver := &metadata.Entity{
		Name:    "Задача",
		Title:   "Задача",
		Kind:    metadata.KindDocument,
		BasedOn: []string{treeSource.Name},
	}

	s, ctx := newSubmitTestServer(t, []*metadata.Entity{source, allowed, denied, treeSource, treeReceiver})
	sourceID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	if err := s.store.Upsert(ctx, source.Name, sourceID, map[string]any{"Номер": "ЗПК-00001"}, source); err != nil {
		t.Fatalf("seed source document: %v", err)
	}
	treeID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	if err := s.store.Upsert(ctx, treeSource.Name, treeID, map[string]any{"Наименование": "Внедрение", "is_folder": false}, treeSource); err != nil {
		t.Fatalf("seed hierarchical source: %v", err)
	}

	user := &auth.User{Roles: []*auth.Role{{
		Name: "Оператор",
		Permissions: auth.Permission{Documents: map[string][]string{
			source.Name:       {"read"},
			allowed.Name:      {"write"},
			treeReceiver.Name: {"write"},
		}, Catalogs: map[string][]string{
			treeSource.Name: {"read"},
		}},
	}}}
	router := chi.NewRouter()
	s.Mount(router)
	renderList := func(target string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status=%d body=%s", target, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	decodeActions := func(body string) []basedOnAction {
		t.Helper()
		match := regexp.MustCompile(`(?s)<script type="application/json" id="ob-list-config">(.*?)</script>`).FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatal("ob-list-config JSON not found")
		}
		var cfg struct {
			BasedOn []basedOnAction `json:"basedOn"`
		}
		if err := json.Unmarshal([]byte(match[1]), &cfg); err != nil {
			t.Fatalf("decode ob-list-config: %v\n%s", err, match[1])
		}
		return cfg.BasedOn
	}

	body := renderList("/ui/document/заказпокупателя")
	if !strings.Contains(body, `data-ob-entity-id="`+sourceID.String()+`"`) {
		t.Fatalf("list row does not expose the selected source ID to delegated actions")
	}

	actions := decodeActions(body)
	if len(actions) != 1 {
		t.Fatalf("based-on actions = %#v, want exactly the writable receiver", actions)
	}
	action := actions[0]
	if action.Label != allowed.Title {
		t.Fatalf("receiver label = %q, want %q", action.Label, allowed.Title)
	}
	if strings.Contains(body, denied.Title) || strings.Contains(strings.ToLower(body), strings.ToLower(denied.Name)) {
		t.Fatal("receiver without write permission leaked into list commands")
	}
	actionURL, err := url.Parse(action.URL)
	if err != nil {
		t.Fatalf("parse receiver URL %q: %v", action.URL, err)
	}
	if !strings.HasSuffix(strings.ToLower(actionURL.Path), "/"+strings.ToLower(allowed.Name)+"/new") {
		t.Fatalf("receiver URL path = %q", actionURL.Path)
	}
	if got := actionURL.Query().Get("based_on"); got != source.Name {
		t.Fatalf("based_on = %q, want %q", got, source.Name)
	}
	if got := actionURL.Query().Get("based_on_id"); got != "" {
		t.Fatalf("server URL already contains row-specific based_on_id=%q", got)
	}

	for _, target := range []string{
		"/ui/document/заказпокупателя?view=tiles&lm=pages",
		"/ui/document/заказпокупателя?lm=feed",
	} {
		modeBody := renderList(target)
		if !strings.Contains(modeBody, `data-ob-entity-id="`+sourceID.String()+`"`) {
			t.Errorf("%s: row lost the source ID", target)
		}
		if got := decodeActions(modeBody); len(got) != 1 || got[0].Label != allowed.Title {
			t.Errorf("%s: based-on actions = %#v", target, got)
		}
	}

	// A live refresh fetches the same list URL and replaces its row container.
	// A newly rendered row must therefore carry the same delegated-action ID.
	secondID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	if err := s.store.Upsert(ctx, source.Name, secondID, map[string]any{"Номер": "ЗПК-00002"}, source); err != nil {
		t.Fatalf("seed live-refresh row: %v", err)
	}
	refreshed := renderList("/ui/document/заказпокупателя?lm=pages")
	for _, id := range []uuid.UUID{sourceID, secondID} {
		if !strings.Contains(refreshed, `data-ob-entity-id="`+id.String()+`"`) {
			t.Errorf("live-refresh response lost source ID %s", id)
		}
	}

	treeBody := renderList("/ui/catalog/проекты?view=tree")
	if !strings.Contains(treeBody, `data-ob-entity-id="`+treeID.String()+`"`) {
		t.Fatal("tree row lost the source ID")
	}
	if got := decodeActions(treeBody); len(got) != 1 || got[0].Label != treeReceiver.Title {
		t.Fatalf("tree based-on actions = %#v", got)
	}
}
