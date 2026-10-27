package ui

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html"
)

// Compare the business table reached by the mounted HTTP router, not a private
// loader. Action cells, detail panels and machine keys are not business columns.
func registerExcelHTML(t *testing.T, body string) ([][]string, string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var table [][]string
	var link string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			if _, ok := deleteMatrixHTMLAttr(n, "data-ob-register-excel"); ok {
				link, _ = deleteMatrixHTMLAttr(n, "href")
			}
		}
		if n.Type == html.ElementNode && n.Data == "tr" {
			var row []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.Data == "th" || c.Data == "td") {
					row = append(row, strings.TrimSpace(infoRegNodeText(c)))
				}
			}
			if len(row) != 0 {
				table = append(table, row)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return table, link
}

func TestRegisterExcelHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		goods := &metadata.Entity{Name: "Goods", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		doc := &metadata.Entity{Name: "Shipment", Kind: metadata.KindDocument, Posting: true, Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}, {Name: "Дата", Type: metadata.FieldTypeDate}}}
		reg := &metadata.Register{Name: "Stock", Dimensions: []metadata.Field{{Name: "Owner", Type: metadata.FieldTypeString}, {Name: "Product", Type: "reference:Goods", RefEntity: "Goods"}}, Resources: []metadata.Field{{Name: "Amount", Title: "Quantity", Type: metadata.FieldTypeNumber}, {Name: "Secret", Type: metadata.FieldTypeNumber}}, Attributes: []metadata.Field{{Name: "Note", Type: metadata.FieldTypeString}}}
		if err := db.Migrate(ctx, []*metadata.Entity{goods, doc}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		product, recorder := uuid.New(), uuid.New()
		period := time.Date(2026, 10, 1, 11, 22, 33, 0, time.UTC)
		if err := db.Upsert(ctx, goods.Name, product, map[string]any{"Наименование": "Chair"}, goods); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, doc.Name, recorder, map[string]any{"Номер": "ORDER-2227", "Дата": period}, doc); err != nil {
			t.Fatal(err)
		}
		if err := db.SetPosted(ctx, doc.Name, recorder, true); err != nil {
			t.Fatal(err)
		}
		var movements []map[string]any
		for _, owner := range []string{"mine", "other"} {
			movements = append(movements, map[string]any{"Owner": owner, "Product": product.String(), "Amount": "123.45", "Secret": "987654.25", "Note": owner + "-note", "ВидДвижения": "Приход"})
		}
		if err := db.WriteMovements(ctx, reg.Name, doc.Name, recorder, movements, reg, &period); err != nil {
			t.Fatal(err)
		}
		var infos []*metadata.InfoRegister
		for _, periodic := range []bool{false, true} {
			ir := &metadata.InfoRegister{Name: fmt.Sprintf("Rates%t", periodic), Periodic: periodic, Dimensions: []metadata.Field{{Name: "Owner", Type: metadata.FieldTypeString}, {Name: "Seq", Type: metadata.FieldTypeNumber, Length: 20, Scale: 2}}, Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}, {Name: "Amount", Type: metadata.FieldTypeNumber}, {Name: "Secret", Type: metadata.FieldTypeString}, {Name: "Product", Type: "reference:Goods", RefEntity: "Goods"}}}
			if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
				t.Fatal(err)
			}
			for _, owner := range []string{"mine", "other"} {
				for i := 1; i <= 3; i++ {
					var at *time.Time
					if periodic {
						at = &period
					}
					if err := db.InfoRegSet(ctx, ir, map[string]any{"Owner": owner, "Seq": fmt.Sprintf("900719925474099%d.01", i)}, map[string]any{"Value": fmt.Sprintf("%s-row-%d", owner, i), "Amount": "-123456789012345", "Secret": "private-resource", "Product": product.String()}, at); err != nil {
						t.Fatal(err)
					}
				}
			}
			infos = append(infos, ir)
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{goods, doc}, Registers: []*metadata.Register{reg}, InfoRegs: infos})
		s := New(registry, db, interpreter.New(), nil, Config{}, nil)
		t.Cleanup(s.Close)
		router := chi.NewRouter()
		s.Mount(router)
		policy := auth.RowPolicy{Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "mine"}}
		permission := auth.Permission{Catalogs: map[string][]string{goods.Name: {"read"}}, Documents: map[string][]string{doc.Name: {"read"}}, Registers: map[string][]string{reg.Name: {"read"}}, InfoRegs: map[string][]string{}, RowAccess: auth.RowAccess{Registers: map[string]auth.RowPolicies{reg.Name: {"read": policy}}, InfoRegs: map[string]auth.RowPolicies{}}, FieldAccess: auth.FieldAccess{Registers: map[string]auth.FieldPolicies{reg.Name: {"Secret": {Read: "mask_all"}}}, InfoRegs: map[string]auth.FieldPolicies{}}}
		for _, ir := range infos {
			permission.InfoRegs[ir.Name] = []string{"read"}
			permission.RowAccess.InfoRegs[ir.Name] = auth.RowPolicies{"read": policy}
			permission.FieldAccess.InfoRegs[ir.Name] = auth.FieldPolicies{"Secret": {Read: "mask_all"}}
		}
		user := &auth.User{ID: "excel-reader", Login: "excel-reader", Roles: []*auth.Role{{Name: "reader", Permissions: permission}}}
		denied := &auth.User{ID: "denied", Roles: []*auth.Role{{Name: "denied"}}}
		paths := []string{"/ui/register/stock", "/ui/register/stock/balances", "/ui/inforeg/ratesfalse", "/ui/inforeg/ratestrue"}
		for _, path := range paths {
			t.Run(path, func(t *testing.T) {
				queries := []string{"flt_Owner=mine&from=2026-10-01&to=2026-10-01&limit=1&page=2", "limit=1&page=1", "limit=1&page=9223372036854775807", "limit=-1&page=-1", "limit=bad&page=bad", "flt_Owner=missing", "to=2026-09-30"}
				for _, query := range queries {
					htmlResponse := infoRegPageRequest(router, user, http.MethodGet, path+"?"+query, nil)
					if htmlResponse.Code != http.StatusOK {
						t.Fatalf("HTML %s: %d %s", query, htmlResponse.Code, htmlResponse.Body.String())
					}
					table, link := registerExcelHTML(t, htmlResponse.Body.String())
					u, err := url.Parse(link)
					if err != nil {
						t.Fatal(err)
					}
					wantQuery, _ := url.ParseQuery(query)
					if link == "" || u.Path != path+"/excel" || u.Query().Encode() != wantQuery.Encode() {
						t.Fatalf("export link lost context: %q", link)
					}
					xlsx := infoRegPageRequest(router, user, http.MethodGet, link, nil)
					if xlsx.Code != http.StatusOK || !strings.Contains(xlsx.Header().Get("Content-Type"), "spreadsheetml") {
						t.Fatalf("XLSX %s: %d %s", link, xlsx.Code, xlsx.Body.String())
					}
					book, err := excelize.OpenReader(bytes.NewReader(xlsx.Body.Bytes()))
					if err != nil {
						t.Fatal(err)
					}
					rows, err := book.GetRows("Sheet1")
					if err != nil {
						t.Fatal(err)
					}
					if len(table) == 0 && len(rows) != 1 {
						t.Fatalf("empty export has data: %v", rows)
					}
					if len(table) != 0 {
						// Period is a typed XLSX date instead of Go's HTML spelling.
						for i := 1; i < len(table); i++ {
							if strings.HasSuffix(path, "ratestrue") {
								at, err := time.Parse("02.01.2006 15:04:05", table[i][0])
								if err != nil {
									t.Fatal(err)
								}
								raw, err := book.GetCellValue("Sheet1", fmt.Sprintf("A%d", i+1), excelize.Options{RawCellValue: true})
								if err != nil {
									t.Fatal(err)
								}
								serial, err := strconv.ParseFloat(raw, 64)
								if err != nil {
									t.Fatal(err)
								}
								excelDate, err := excelize.ExcelDateToTime(serial, false)
								if err != nil || excelDate.Format("2006-01-02 15:04:05") != at.Format("2006-01-02 15:04:05") {
									t.Fatalf("date changed: HTML %s XLSX %s (%v)", at, excelDate, err)
								}
								table[i][0] = rows[i][0]
							}
							for j := range table[i] {
								table[i][j] = strings.TrimPrefix(table[i][j], "▲ ")
							}
						}
						if !reflect.DeepEqual(rows, table) {
							t.Fatalf("HTML/XLSX differ for %s: \nHTML %v\nXLSX %v", link, table, rows)
						}
					}
					serialized := fmt.Sprint(rows)
					for _, secret := range []string{"other", "private-resource", "987654", product.String(), recorder.String(), "period_key", storage.InfoRegKeyValuesField} {
						if strings.Contains(serialized, secret) {
							t.Fatalf("export leaked %q: %v", secret, rows)
						}
					}
					if len(rows) > 1 {
						for col, header := range rows[0] {
							cell, _ := excelize.CoordinatesToCellName(col+1, 2)
							kind, _ := book.GetCellType("Sheet1", cell)
							if (header == "Quantity" || header == "Amount" || header == "Период") && kind != excelize.CellTypeNumber && kind != excelize.CellTypeUnset {
								t.Fatalf("%s not typed numeric/date: %v", header, kind)
							}
						}
					}
					if err := book.Close(); err != nil {
						t.Fatal(err)
					}
				}
				for _, target := range []string{path, path + "/excel"} {
					w := infoRegPageRequest(router, denied, http.MethodGet, target, nil)
					if w.Code != http.StatusForbidden {
						t.Fatalf("read denied: %s returned %d", target, w.Code)
					}
				}
			})
		}
		// Protected inputs and reference/registrar representations use the same
		// fail-closed boundary in HTML and XLSX.
		permission.FieldAccess.Registers[reg.Name]["Product"] = auth.FieldPolicy{Read: "mask_all"}
		permission.FieldAccess.Registers[reg.Name]["recorder"] = auth.FieldPolicy{Read: "hide"}
		permission.FieldAccess.Registers[reg.Name]["Note"] = auth.FieldPolicy{Read: "hide"}
		for _, path := range []string{"/ui/register/stock/balances", "/ui/register/stock/excel?flt_Product=x", "/ui/register/stock/balances/excel"} {
			if w := infoRegPageRequest(router, user, http.MethodGet, path, nil); w.Code != http.StatusForbidden {
				t.Fatalf("protected register input: %s returned %d", path, w.Code)
			}
		}
		for _, ir := range infos {
			permission.FieldAccess.InfoRegs[ir.Name]["Owner"] = auth.FieldPolicy{Read: "mask_all"}
			permission.FieldAccess.InfoRegs[ir.Name]["Seq"] = auth.FieldPolicy{Read: "mask_all"}
			permission.FieldAccess.InfoRegs[ir.Name]["Product"] = auth.FieldPolicy{Read: "mask_all"}
			path := "/ui/inforeg/" + strings.ToLower(ir.Name) + "/excel?flt_Owner=&flt_Owner=mine"
			if w := infoRegPageRequest(router, user, http.MethodGet, path, nil); w.Code != http.StatusForbidden {
				t.Fatalf("protected inforeg input: %s returned %d", path, w.Code)
			}
		}
		for _, path := range []string{"/ui/register/stock/excel", "/ui/inforeg/ratestrue/excel", "/ui/inforeg/ratesfalse/excel"} {
			w := infoRegPageRequest(router, user, http.MethodGet, path, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("masked export %s: %d %s", path, w.Code, w.Body.String())
			}
			book, err := excelize.OpenReader(bytes.NewReader(w.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			rows, err := book.GetRows("Sheet1")
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"Chair", "ORDER-2227", "mine-note", "Note", product.String(), recorder.String(), "900719925474099", "period_key", storage.InfoRegKeyValuesField} {
				if strings.Contains(fmt.Sprint(rows), secret) {
					t.Fatalf("masked export %s leaked %s: %v", path, secret, rows)
				}
			}
			if err := book.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
