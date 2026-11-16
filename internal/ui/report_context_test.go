package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/xuri/excelize/v2"
)

func TestReportTrustedContextHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		repo := auth.NewRepo(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			t.Fatal(err)
		}
		user, err := repo.Create(ctx, "context-alice", "test-context-password", "Alice", true)
		if err != nil {
			t.Fatal(err)
		}
		card := &metadata.Entity{Name: "ContextStaff", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Account", Type: "reference:_users", RefEntity: "_users"}, {Name: "Department", Type: metadata.FieldTypeString},
		}}
		row := &metadata.Entity{Name: "ContextEntry", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString}, {Name: "Owner", Type: metadata.FieldTypeString}, {Name: "Department", Type: metadata.FieldTypeString},
		}}
		if err := db.Migrate(ctx, []*metadata.Entity{card, row}); err != nil {
			t.Fatal(err)
		}
		cardID := uuid.New()
		if err := db.Upsert(ctx, card.Name, cardID, map[string]any{"Account": user.ID, "Department": "OWN_UNIT"}, card); err != nil {
			t.Fatal(err)
		}
		for _, data := range []map[string]any{
			{"Name": "VISIBLE_CONTEXT_ROW", "Owner": user.Login, "Department": "OWN_UNIT"},
			{"Name": "FOREIGN_USER_ROW", "Owner": "bob", "Department": "OWN_UNIT"},
			{"Name": "FOREIGN_UNIT_ROW", "Owner": user.Login, "Department": "OTHER_UNIT"},
		} {
			if err := db.Upsert(ctx, row.Name, uuid.New(), data, row); err != nil {
				t.Fatal(err)
			}
		}
		rep := &reportpkg.Report{Name: "TrustedContext", Query: `ВЫБРАТЬ Name ИЗ Справочник.ContextEntry ГДЕ Owner=&Actor И Department=&Unit`, Params: []reportpkg.Param{
			{Name: "Actor", Type: "string", Context: &reportpkg.ParamContext{User: "login"}},
			{Name: "Unit", Type: "string", Context: &reportpkg.ParamContext{UserCard: &auth.UserCardSource{Catalog: card.Name, AccountField: "Account", Field: "Department"}}},
		}}
		user.IsAdmin = false
		user.Roles = []*auth.Role{{Name: "ContextRunner", Permissions: auth.Permission{Reports: map[string][]string{rep.Name: {"run"}}, Catalogs: map[string][]string{row.Name: {"read"}}}}}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{card, row}, Reports: []*reportpkg.Report{rep}})
		s := &Server{store: db, reg: reg, interp: interpreter.New(), lockMgr: runtime.NewLockManager(), messages: NewMessageStore(), ops: newOperationLimiter()}
		t.Cleanup(s.Close)
		request := func(method, target string, form url.Values) *http.Request {
			r := reqWithChi(method, target, form, map[string]string{"name": rep.Name, "format": "excel"})
			return r.WithContext(auth.ContextWithUser(r.Context(), user))
		}
		assertRows := func(body string) {
			t.Helper()
			if !strings.Contains(body, "VISIBLE_CONTEXT_ROW") || strings.Contains(body, "FOREIGN_USER_ROW") || strings.Contains(body, "FOREIGN_UNIT_ROW") {
				t.Fatal("report context did not constrain actual rows")
			}
		}
		spoof := url.Values{"Actor": {"bob"}, "actor": {"bob"}, "Unit": {"OTHER_UNIT"}}
		t.Run("post-spoof", func(t *testing.T) {
			w := httptest.NewRecorder()
			s.reportRun(w, request("POST", "/ui/report/TrustedContext", spoof))
			if w.Code != 200 {
				t.Fatalf("run: %d %s", w.Code, w.Body.String())
			}
			assertRows(w.Body.String())
			for _, key := range []string{`name="Actor"`, `name="Unit"`, `Actor=`, `Unit=`} {
				if strings.Contains(w.Body.String(), key) {
					t.Fatalf("context leaked into input or export URL: %s", key)
				}
			}
		})
		t.Run("get-spoof", func(t *testing.T) {
			w := httptest.NewRecorder()
			s.reportForm(w, request("GET", "/ui/report/TrustedContext?__run=1&"+spoof.Encode(), nil))
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
			assertRows(w.Body.String())
		})
		xlsxRows := func(data []byte) {
			t.Helper()
			f, err := excelize.OpenReader(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}()
			rs, err := f.GetRows(f.GetSheetList()[0])
			if err != nil {
				t.Fatal(err)
			}
			var b strings.Builder
			for _, r := range rs {
				b.WriteString(strings.Join(r, "|"))
			}
			assertRows(b.String())
		}
		t.Run("excel-spoof", func(t *testing.T) {
			w := httptest.NewRecorder()
			s.reportExcel(w, request("GET", "/ui/report/TrustedContext/excel?"+spoof.Encode(), nil))
			if w.Code != 200 {
				t.Fatalf("excel:%d %s", w.Code, w.Body.String())
			}
			xlsxRows(w.Body.Bytes())
		})
		t.Run("pdf-spoof", func(t *testing.T) {
			w := httptest.NewRecorder()
			s.reportPDF(w, request("GET", "/ui/report/TrustedContext/pdf?"+spoof.Encode(), nil))
			if w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-")) {
				t.Fatalf("pdf:%d", w.Code)
			}
		})
		t.Run("background-excel-spoof", func(t *testing.T) {
			r := request("GET", "/ui/report/TrustedContext/export/excel?"+spoof.Encode(), nil)
			w := httptest.NewRecorder()
			s.reportExportJobStart(w, r)
			if w.Code != 303 {
				t.Fatalf("queue:%d", w.Code)
			}
			jobID := strings.TrimPrefix(w.Header().Get("Location"), "/ui/export-jobs/")
			deadline := time.Now().Add(10 * time.Second)
			for {
				j, ok := s.exportJobStore().get(jobID)
				if ok && j.Status == exportJobDone {
					xlsxRows(j.Data)
					break
				}
				if ok && j.Status == exportJobError {
					t.Fatal(j.Error)
				}
				if time.Now().After(deadline) {
					t.Fatal("export did not complete")
				}
				time.Sleep(time.Millisecond * 10)
			}
		})
		t.Run("ambiguous-card-refused", func(t *testing.T) {
			if err := db.Upsert(ctx, card.Name, uuid.New(), map[string]any{"Account": user.ID, "Department": "OTHER_UNIT"}, card); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			s.reportRun(w, request("POST", "/ui/report/TrustedContext", spoof))
			if w.Code != 403 || strings.Contains(w.Body.String(), "VISIBLE_CONTEXT_ROW") {
				t.Fatal("ambiguous card did not fail closed")
			}
		})
	})
}
