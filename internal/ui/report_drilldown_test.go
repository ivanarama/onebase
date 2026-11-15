package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

func TestReportTypedDrilldownHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		db.SetFilesDir(t.TempDir())
		if err := db.EnsureBlobTable(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.EnsureAttachmentTable(ctx); err != nil {
			t.Fatal(err)
		}
		docA := &metadata.Entity{Name: "TypedInvoice", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Label", Type: metadata.FieldTypeString}, {Name: "Owner", Type: metadata.FieldTypeString}, {Name: "Photo", Type: metadata.FieldTypeImage}}}
		docB := &metadata.Entity{Name: "TypedCall", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Label", Type: metadata.FieldTypeString}, {Name: "Owner", Type: metadata.FieldTypeString}}}
		task := &metadata.Entity{Name: "TypedTask", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Label", Type: metadata.FieldTypeString}, {Name: "Owner", Type: metadata.FieldTypeString}}}
		source := &metadata.Entity{Name: "TypedReportRow", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "DocCell", Type: metadata.FieldTypeString}, {Name: "TargetID", Type: metadata.FieldTypeString}, {Name: "TargetType", Type: metadata.FieldTypeString},
			{Name: "TaskID", Type: metadata.FieldTypeString}, {Name: "BlobID", Type: metadata.FieldTypeString}, {Name: "AttachmentID", Type: metadata.FieldTypeString}, {Name: "CallID", Type: metadata.FieldTypeString},
		}}
		entities := []*metadata.Entity{docA, docB, task, source}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		invoiceID, callID, taskID, foreignID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		for _, row := range []struct {
			entity       *metadata.Entity
			id           uuid.UUID
			owner, label string
		}{{docA, invoiceID, "alice", "Invoice"}, {docA, foreignID, "bob", "Foreign"}, {docB, callID, "alice", "Call"}, {task, taskID, "alice", "Task"}} {
			if err := db.Upsert(ctx, row.entity.Name, row.id, map[string]any{"Owner": row.owner, "Label": row.label}, row.entity); err != nil {
				t.Fatal(err)
			}
		}
		blob, err := db.PutBlob(ctx, "image/png", strings.NewReader("synthetic-image"), 1024, storage.BlobOwner{Kind: "document", Entity: docA.Name})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, docA.Name, invoiceID, map[string]any{"Owner": "alice", "Label": "Invoice", "Photo": blob.ID.String()}, docA); err != nil {
			t.Fatal(err)
		}
		att, err := db.UploadAttachment(ctx, "document", docA.Name, invoiceID, "recording.ogg", "audio/ogg", "bob", strings.NewReader("synthetic-recording"), 1024)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []struct {
			typ   string
			id    uuid.UUID
			label string
		}{{"invoice", invoiceID, "VISIBLE_INVOICE_CELL"}, {"call", callID, "VISIBLE_CALL_CELL"}} {
			if err := db.Upsert(ctx, source.Name, uuid.New(), map[string]any{"DocCell": target.label, "TargetType": target.typ, "TargetID": target.id.String(), "TaskID": taskID.String(), "BlobID": blob.ID.String(), "AttachmentID": att.ID.String(), "CallID": callID.String()}, source); err != nil {
				t.Fatal(err)
			}
		}
		rep := &reportpkg.Report{Name: "TypedActions", Query: `ВЫБРАТЬ DocCell, TargetID, TargetType, TaskID, BlobID, AttachmentID, CallID, 'Photo' КАК Photo, 'Recording' КАК Recording, 'Call' КАК Call ИЗ Справочник.TypedReportRow`, Drilldowns: []reportpkg.Drilldown{
			{Field: "DocCell", IDField: "TargetID", TypeField: "TargetType", Targets: map[string]reportpkg.DrilldownTarget{"invoice": {Kind: "entity", Entity: docA.Name}, "call": {Kind: "entity", Entity: docB.Name}}},
			{Field: "Photo", IDField: "BlobID", Target: &reportpkg.DrilldownTarget{Kind: "image"}},
			{Field: "Recording", IDField: "AttachmentID", Target: &reportpkg.DrilldownTarget{Kind: "attachment"}},
			{Title: "Open task", IDField: "TaskID", Target: &reportpkg.DrilldownTarget{Kind: "entity", Entity: task.Name}},
			{Field: "Call", IDField: "CallID", Target: &reportpkg.DrilldownTarget{Kind: "entity", Entity: docB.Name}},
		}}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: entities, Reports: []*reportpkg.Report{rep}})
		s := &Server{store: db, reg: reg, interp: interpreter.New(), lockMgr: runtime.NewLockManager(), messages: NewMessageStore(), ops: newOperationLimiter()}
		t.Cleanup(s.Close)
		policy := auth.RowPolicy{Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}
		role := &auth.Role{Name: "TypedReader", Permissions: auth.Permission{Reports: map[string][]string{rep.Name: {"run"}}, Documents: map[string][]string{docA.Name: {"read"}, docB.Name: {"read"}}, Catalogs: map[string][]string{source.Name: {"read"}, task.Name: {"read"}}, RowAccess: auth.RowAccess{Documents: map[string]auth.RowPolicies{docA.Name: {"read": policy}, docB.Name: {"read": policy}}, Catalogs: map[string]auth.RowPolicies{task.Name: {"read": policy}}}}}
		user := &auth.User{ID: uuid.NewString(), Login: "alice", Roles: []*auth.Role{role}}
		router := chi.NewRouter()
		s.Mount(router)
		run := func(method, target string) *httptest.ResponseRecorder {
			t.Helper()
			r := httptest.NewRequest(method, target, nil)
			r = r.WithContext(auth.ContextWithUser(r.Context(), user))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			return w
		}
		links := func(body string) []string {
			t.Helper()
			root, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "a" {
					class, href := "", ""
					for _, a := range n.Attr {
						if a.Key == "class" {
							class = a.Val
						}
						if a.Key == "href" {
							href = a.Val
						}
					}
					if class == "report-drilldown" {
						out = append(out, href)
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			walk(root)
			return out
		}
		var generated []string
		for _, mode := range []string{"flat", "composed"} {
			t.Run(mode, func(t *testing.T) {
				if mode == "composed" {
					rep.Composition = &reportpkg.Composition{Groupings: []string{"targettype"}, Detail: true, Measures: []reportpkg.Measure{{Field: "doccell", Agg: "count"}, {Field: "photo", Agg: "count"}, {Field: "recording", Agg: "count"}, {Field: "call", Agg: "count"}}}
				} else {
					rep.Composition = nil
				}
				w := run("POST", "/ui/report/TypedActions")
				if w.Code != 200 {
					t.Fatalf("report:%d %s", w.Code, w.Body.String())
				}
				got := links(w.Body.String())
				if len(got) != 10 {
					t.Fatalf("got %d actions, want five per row", len(got))
				}
				generated = got
				for _, href := range got {
					u, err := url.Parse(href)
					if err != nil {
						t.Fatal(err)
					}
					want := ""
					switch u.Query().Get("d") {
					case "0":
						if u.Query().Get("type") == "invoice" {
							want = "/ui/document/TypedInvoice/" + invoiceID.String()
						} else {
							want = "/ui/document/TypedCall/" + callID.String()
						}
					case "1":
						want = "/ui/_image/" + blob.ID.String()
					case "2":
						want = "/ui/attachments/" + att.ID.String() + "/download"
					case "3":
						want = "/ui/catalog/TypedTask/" + taskID.String()
					case "4":
						want = "/ui/document/TypedCall/" + callID.String()
					default:
						t.Fatal("unknown action")
					}
					click := run("GET", href)
					if click.Code != 303 || click.Header().Get("Location") != want {
						t.Fatalf("typed target:%d %s, want %s", click.Code, click.Header().Get("Location"), want)
					}
					if next := run("GET", want); next.Code != 200 {
						t.Fatalf("protected target:%s status%d", want, next.Code)
					}
				}
			})
		}
		t.Run("forged-selector", func(t *testing.T) {
			w := run("GET", "/ui/report/TypedActions/drilldown?d=0&type=Unknown&id="+invoiceID.String()+"&entity=TypedInvoice&url=https://example.invalid")
			if w.Code != 404 || w.Header().Get("Location") != "" {
				t.Fatal("client supplied a target")
			}
		})
		t.Run("forged-row-id", func(t *testing.T) {
			w := run("GET", "/ui/report/TypedActions/drilldown?d=0&type=invoice&id="+foreignID.String())
			if w.Code != 404 {
				t.Fatal("target RLS bypass")
			}
		})
		t.Run("invalid-id-and-index", func(t *testing.T) {
			for _, q := range []string{"d=0&type=invoice&id=not-a-uuid", "d=999&id=" + invoiceID.String(), "d=-1&id=" + invoiceID.String()} {
				if w := run("GET", "/ui/report/TypedActions/drilldown?"+q); w.Code != 404 {
					t.Fatal("invalid ID/index became a target")
				}
			}
		})
		t.Run("live-rights-revoked", func(t *testing.T) {
			delete(role.Permissions.Documents, docA.Name)
			delete(role.Permissions.Documents, docB.Name)
			delete(role.Permissions.Catalogs, task.Name)
			for _, href := range generated {
				if w := run("GET", href); w.Code != 404 {
					t.Fatalf("stale link retained permission: %d", w.Code)
				}
			}
		})
		t.Run("report-run-revoked", func(t *testing.T) {
			delete(role.Permissions.Reports, rep.Name)
			if w := run("GET", generated[0]); w.Code != 403 {
				t.Fatalf("report permission bypass: %d", w.Code)
			}
		})
	})
}
