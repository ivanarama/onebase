package launcher

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

type configuratorContextReader struct{ actor, unit string }

func (r configuratorContextReader) ReadUserCardSource(_ context.Context, d storage.UserCardDescriptor, actor string) (storage.UserCardValue, error) {
	if actor != r.actor {
		return storage.UserCardValue{}, fmt.Errorf("client actor used for card read")
	}
	value := any("server-unit-name")
	if d.Field.Name == "Department" {
		value = r.unit
	}
	return storage.UserCardValue{Available: true, Value: value}, nil
}

// Native GET -> browser controls -> POST must not turn a trusted parameter into
// an editable one. The form may never create/replace a context definition.
func TestConfiguratorSaveReport_ContextRoundTrip(t *testing.T) {
	const original = `name: TrustedContext
title: Original
params:
  - {name: ActorID, type: 'reference:_users', context: {user: id}}
  - {name: ActorLogin, type: string, context: {user: login}}
  - name: Unit
    type: reference:Departments
    context: {user_card: {catalog: Staff, account_field: Account, field: Department}}
  - {name: ShortUnit, type: string, context: {user_card: Staff.ShortName}}
  - {name: Input, type: string, default: ordinary}
query: ВЫБРАТЬ 1 КАК Value
`
	before, err := report.ParseBytes([]byte(original))
	if err != nil {
		t.Fatal(err)
	}
	contexts := map[string]*report.ParamContext{}
	for _, p := range before.Params {
		contexts[strings.ToLower(p.Name)] = p.Context
	}
	actorID, unitID := uuid.NewString(), uuid.NewString()
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{{Name: "Staff", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Account", Type: "reference:_users", RefEntity: "_users"}, {Name: "Department", Type: "reference:Departments", RefEntity: "Departments"}, {Name: "ShortName", Type: metadata.FieldTypeString},
	}}}})
	for _, source := range []string{"file", "database"} {
		for _, action := range []string{"title-query", "reorder", "case-only", "forged-context", "rename-refused", "delete-refused", "type-change-refused", "duplicate-refused", "type-change-chart-refused"} {
			t.Run(source+"/"+action, func(t *testing.T) {
				h, dir := newFileBaseHandler(t)
				h.runner = NewRunner()
				var readReport func() []byte
				if source == "file" {
					path := writeCfgFile(t, dir, "reports", "trustedcontext.yaml", original)
					readReport = func() []byte {
						t.Helper()
						b, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						return b
					}
				} else {
					base, err := h.store.Get("test")
					if err != nil {
						t.Fatal(err)
					}
					base.ConfigSource = "database"
					base.DBType = "sqlite"
					base.DBPath = filepath.Join(t.TempDir(), "config.db")
					if err := h.store.Update(base); err != nil {
						t.Fatal(err)
					}
					db, err := OpenDB(context.Background(), base)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { db.Close() })
					repo := configdb.New(db)
					if err := repo.EnsureSchema(context.Background()); err != nil {
						t.Fatal(err)
					}
					if err := repo.SaveFiles(context.Background(), []configdb.ConfigFile{{Path: "config/app.yaml", Content: []byte("name: Test\n")}, {Path: "reports/trustedcontext.yaml", Content: []byte(original)}}, configdb.VersionOptions{Message: "seed trusted report"}); err != nil {
						t.Fatal(err)
					}
					readReport = func() []byte {
						t.Helper()
						b, found, err := repo.ReadFile(context.Background(), "reports/trustedcontext.yaml")
						if err != nil || !found {
							t.Fatalf("read report:%t %v", found, err)
						}
						return b
					}
				}
				router := chi.NewRouter()
				router.Get("/bases/{id}/configurator", h.configuratorPage)
				router.Post("/bases/{id}/configurator/report", h.configuratorSaveReport)
				get := httptest.NewRecorder()
				router.ServeHTTP(get, httptest.NewRequest("GET", "/bases/test/configurator?tab=tree", nil))
				if get.Code != 200 {
					t.Fatal(get.Code)
				}
				form := browserSubmit(t, get.Body.String(), "/configurator/report")
				// reference:_users is not an ordinary project entity select option.
				// The native control must still submit the original exact type.
				if form.Get("param.0.type") != "reference:_users" || form.Get("param.2.type") != "reference:Departments" {
					t.Fatalf("context type lost on GET: %v", form)
				}
				form.Set("title", "Changed")
				form.Set("query", "ВЫБРАТЬ 2 КАК Value")
				refused := strings.HasSuffix(action, "refused")
				switch action {
				case "reorder":
					for _, field := range []string{"name", "type", "label"} {
						a, b := form.Get("param.0."+field), form.Get("param.2."+field)
						form.Set("param.0."+field, b)
						form.Set("param.2."+field, a)
					}
				case "case-only":
					form.Set("param.1.name", "actorlogin")
				case "forged-context":
					form.Set("param.0.context", "null")
					form.Set("param.1.context.user", "id")
					form.Set("param.2.context.user_card", "Other.Private")
					form.Set("param.4.context", "{user: login}")
				case "rename-refused":
					form.Set("param.1.name", "NewActor")
				case "delete-refused":
					form.Set("param.2.name", "")
				case "type-change-refused":
					form.Set("param.2.type", "string")
				case "duplicate-refused":
					form.Set("param.8.name", "ACTORLOGIN")
					form.Set("param.8.type", "string")
				case "type-change-chart-refused":
					form.Set("param.0.type", "string")
					form.Set("chart_source", "// must not be written on rejected save")
				}
				req := httptest.NewRequest("POST", "/bases/test/configurator/report", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("X-Onebase-Ajax", "1")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				ok, reason := cfgResponse(t, w)
				if refused {
					if ok || reason == "" {
						t.Fatalf("unsafe edit reported saved:%s", w.Body.String())
					}
					if !bytes.Equal(readReport(), []byte(original)) {
						t.Fatal("refused save changed trusted YAML")
					}
					if source == "file" {
						if _, err := os.Stat(filepath.Join(dir, "src", "TrustedContext.rep.os")); !os.IsNotExist(err) {
							t.Fatal("rejected report save wrote module source")
						}
					}
					return
				}
				if !ok {
					t.Fatalf("safe native save refused:%s", reason)
				}
				after, err := report.ParseBytes(readReport())
				if err != nil {
					t.Fatal(err)
				}
				if len(after.Params) != 5 || after.Title != "Changed" || after.Query != "ВЫБРАТЬ 2 КАК Value" {
					t.Fatal("safe edit not persisted")
				}
				for _, p := range after.Params {
					if !reflect.DeepEqual(p.Context, contexts[strings.ToLower(p.Name)]) {
						t.Fatalf("context changed for %s: %#v", p.Name, p.Context)
					}
				}
				actorCtx := auth.ContextWithUser(context.Background(), &auth.User{ID: actorID, Login: "server-alice"})
				spoof := map[string]any{"ActorID": uuid.NewString(), "ActorLogin": "client-bob", "actorlogin": "client-bob", "Unit": uuid.NewString(), "ShortUnit": "client-unit", "Input": "ordinary-input"}
				bound, err := report.ResolveContextParams(actorCtx, after, spoof, reg, configuratorContextReader{actor: actorID, unit: unitID})
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range after.Params {
					var want any
					switch strings.ToLower(p.Name) {
					case "actorid":
						want = actorID
					case "actorlogin":
						want = "server-alice"
					case "unit":
						want = unitID
					case "shortunit":
						want = "server-unit-name"
					default:
						want = "ordinary-input"
					}
					if bound[p.Name] != want {
						t.Fatalf("saved context accepted client spoof for %s: %#v", p.Name, bound[p.Name])
					}
				}
				get = httptest.NewRecorder()
				router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/bases/test/configurator?tab=tree", nil))
				if get.Code != 200 {
					t.Fatal(get.Code)
				}
				reopened := browserSubmit(t, get.Body.String(), "/configurator/report")
				for i, p := range after.Params {
					if reopened.Get(fmt.Sprintf("param.%d.type", i)) != p.Type {
						t.Fatalf("reopened native type changed for %s", p.Name)
					}
				}
			})
		}
	}
}
