package ui

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"context"
	"io"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// This tests output noninterference, not a search over compressed binary data:
// changing only PII and the arbitrary stored label must not change any XLSX XML
// entry or PDF drawing instruction, including text wrapped across PDF lines.
func TestReportOutputPrivacyHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{Name: "SensitiveDoc", Kind: metadata.KindDocument, Presentation: []string{"Label"}, Fields: []metadata.Field{
			{Name: "Label", Type: metadata.FieldTypeString}, {Name: "Phone", Type: metadata.FieldTypeString, PII: true}, {Name: "Address", Type: metadata.FieldTypeString, PII: true}, {Name: "Amount", Type: metadata.FieldTypeNumber},
		}}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		rep := &reportpkg.Report{Name: "Privacy", Query: "ВЫБРАТЬ Ссылка КАК Doc, Amount ИЗ Документ.SensitiveDoc"}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}, Reports: []*reportpkg.Report{rep}})
		s := &Server{store: db, reg: reg, interp: interpreter.New(), lockMgr: runtime.NewLockManager(), messages: NewMessageStore(), ops: newOperationLimiter()}
		id := uuid.New()
		write := func(label, phone, address string) {
			t.Helper()
			if err := db.Upsert(ctx, entity.Name, id, map[string]any{"Label": label, "Phone": phone, "Address": address, "Amount": 12345}, entity); err != nil {
				t.Fatal(err)
			}
		}
		request := func(kind string, user *auth.User) []byte {
			t.Helper()
			r := reqWithChi("GET", "/ui/report/Privacy?__run=1", nil, map[string]string{"name": rep.Name})
			if user != nil {
				r = r.WithContext(auth.ContextWithUser(r.Context(), user))
			}
			w := httptest.NewRecorder()
			switch kind {
			case "html":
				s.reportForm(w, r)
			case "xlsx":
				s.reportExcel(w, r)
			case "pdf":
				s.reportPDF(w, r)
			}
			if w.Code != 200 {
				t.Fatalf("%s: %d %s", kind, w.Code, w.Body.String())
			}
			return w.Body.Bytes()
		}
		xml := func(data []byte) string {
			t.Helper()
			z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			var all strings.Builder
			for _, f := range z.File {
				if !strings.HasPrefix(f.Name, "xl/") {
					continue
				}
				r, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				b, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil {
					t.Fatal(err)
				}
				all.WriteString(f.Name)
				all.Write(b)
			}
			return all.String()
		}
		pdfPages := func(data []byte) string {
			t.Helper()
			var all strings.Builder
			// Page streams written by fpdf carry a direct length. Read that many
			// bytes so a final 0x0d in the compressed payload stays part of it.
			streamHeader := regexp.MustCompile(`<</Filter /FlateDecode /Length ([0-9]+)>>\r?\nstream\r?\n`)
			for _, stream := range streamHeader.FindAllSubmatchIndex(data, -1) {
				start := stream[1]
				length, err := strconv.Atoi(string(data[stream[2]:stream[3]]))
				if err != nil || length > len(data)-start {
					t.Fatalf("invalid length for PDF stream at byte %d", start)
				}
				r, err := zlib.NewReader(bytes.NewReader(data[start : start+length]))
				if err != nil {
					t.Fatal(err)
				}
				b, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(b, []byte("BT ")) {
					all.Write(b)
				}
			}
			if all.Len() == 0 {
				t.Fatal("no PDF page text found")
			}
			return all.String()
		}
		otkPermission := auth.Permission{
			Reports:   map[string][]string{rep.Name: {"run"}},
			Documents: map[string][]string{entity.Name: {"read", "disclose"}},
			FieldAccess: auth.FieldAccess{Documents: map[string]auth.FieldPolicies{
				entity.Name: {"Phone": {Read: "full"}, "Address": {Read: "full"}},
			}},
		}
		users := []*auth.User{{IsAdmin: true}, {Login: "otk", Roles: []*auth.Role{{Permissions: otkPermission}}}}
		for _, user := range users {
			for _, composed := range []bool{false, true} {
				if composed {
					rep.Composition = &reportpkg.Composition{Groupings: []string{"Doc"}, Measures: []reportpkg.Measure{{Field: "Amount", Agg: "sum"}}, Detail: true, DetailLink: "Doc", DetailEntity: entity.Name}
				} else {
					rep.Composition = nil
				}
				write("Baseline", "BASE_PHONE", "BASE_ADDRESS")
				baselineHTML := string(request("html", user))
				baselineXLSX := xml(request("xlsx", user))
				baselinePDF := pdfPages(request("pdf", user))
				if !strings.Contains(baselineHTML, id.String()) || !strings.Contains(baselineXLSX, "12345") {
					t.Fatal("lost UUID or ordinary measure")
				}
				write("Request 79990009987 HOUSE_987 FLAT_654", "79990009987", "HOUSE_987 FLAT_654")
				changedHTML := string(request("html", user))
				if strings.Contains(changedHTML, "79990009987") || strings.Contains(changedHTML, "HOUSE_987") || strings.Contains(changedHTML, "FLAT_654") {
					t.Fatal("PII in report HTML")
				}
				if changedHTML != baselineHTML {
					t.Fatal("changing only PII/label changed report HTML")
				}
				if xml(request("xlsx", user)) != baselineXLSX {
					t.Fatal("changing only PII/label changed XLSX XML/relationships")
				}
				if pdfPages(request("pdf", user)) != baselinePDF {
					t.Fatal("changing only PII/label changed PDF drawing instructions")
				}
			}
		}
		// Stored data and ordinary privileged cards retain their previous policy.
		row, err := db.GetByID(ctx, entity.Name, id, entity)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.maskedRecordLabel(auth.ContextWithUser(ctx, users[0]), entity, row); !strings.Contains(got, "79990009987") {
			t.Fatal("ordinary card label changed")
		}
	})
}

func TestReportOutputPrivacyUsesSourceKindHTTP(t *testing.T) {
	cat := &metadata.Entity{Name: "Shared", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Phone", Type: metadata.FieldTypeString}}}
	ir := &metadata.InfoRegister{Name: "Shared", Dimensions: []metadata.Field{{Name: "Phone", Type: metadata.FieldTypeString, PII: true}}}
	rep := &reportpkg.Report{Name: "KindPrivacy", Query: "ВЫБРАТЬ Phone ИЗ РегистрСведений.Shared ГДЕ Phone = &Value", Params: []reportpkg.Param{{Name: "Value", Type: "string"}}}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{cat})
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{cat}, InfoRegs: []*metadata.InfoRegister{ir}, Reports: []*reportpkg.Report{rep}})
	w := httptest.NewRecorder()
	r := reqWithChi("GET", "/ui/report/KindPrivacy?__run=1&Value=synthetic", nil, map[string]string{"name": rep.Name})
	r = r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{IsAdmin: true}))
	s.reportForm(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "защищённое поле") {
		t.Fatalf("mandatory source-kind gate missing: status %d", w.Code)
	}
}
