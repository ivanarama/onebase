package ui

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// Load the shipped Trade configuration and post actual documents through the
// public service used by forms/REST. Do not duplicate or call the FIFO helper.
func TestTradeTransfer_OrganizationBalances2417(t *testing.T) {
	for _, tc := range []struct {
		name, receiptOrg, transferOrg string
		distractor                    bool
		quantity                      float64
		beforeReceipt, wantFailure    bool
	}{
		{name: "both_empty", quantity: 7},
		{name: "same_company", receiptOrg: "A", transferOrg: "A", quantity: 7},
		{name: "empty_ignores_company", distractor: true, quantity: 7},
		{name: "company_ignores_empty", receiptOrg: "A", transferOrg: "A", distractor: true, quantity: 7},
		{name: "empty_cannot_spend_company", receiptOrg: "A", quantity: 7, wantFailure: true},
		{name: "company_cannot_spend_empty", transferOrg: "A", quantity: 7, wantFailure: true},
		{name: "other_company", receiptOrg: "A", transferOrg: "B", quantity: 7, wantFailure: true},
		{name: "insufficient_empty", quantity: 29, wantFailure: true},
		{name: "before_receipt", quantity: 7, beforeReceipt: true, wantFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				proj, err := project.Load("../../examples/trade")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(proj.Close)
				if err := db.Migrate(ctx, proj.Entities); err != nil {
					t.Fatal(err)
				}
				if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
					t.Fatal(err)
				}
				if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
					t.Fatal(err)
				}
				if err := db.MigrateConstants(ctx, proj.Constants); err != nil {
					t.Fatal(err)
				}
				s, reg, err := NewOfflineServer(proj, db)
				if err != nil {
					t.Fatal(err)
				}
				svc := s.EntitySvc()
				save := func(entity string, fields map[string]any, rows []map[string]any, post bool) uuid.UUID {
					t.Helper()
					id := uuid.New()
					req := entityservice.SaveRequest{Entity: reg.GetEntity(entity), ID: id, IsNew: true, Fields: fields}
					if rows != nil {
						req.TablePartRows = map[string][]map[string]any{"Товары": rows}
					}
					if post {
						req.Action = "post"
					}
					res, err := svc.Save(ctx, req)
					if err != nil || res.DSLError != "" {
						t.Fatalf("save %s: err=%v DSL=%s", entity, err, res.DSLError)
					}
					return id
				}
				catalog := func(entity, title string) uuid.UUID {
					return save(entity, map[string]any{"Наименование": title}, nil, false)
				}
				source := catalog("Склад", "Source")
				target := catalog("Склад", "Target")
				item := catalog("Номенклатура", "Item")
				orgs := map[string]any{"": nil, "A": catalog("Организация", "Company A").String(), "B": catalog("Организация", "Company B").String()}
				date := time.Date(2026, 11, 17, 9, 0, 0, 0, time.Local)
				receipt := func(org string, price float64, at time.Time) {
					save("ОприходованиеТоваров", map[string]any{
						"Дата": at, "Склад": source.String(), "Организация": orgs[org],
					}, []map[string]any{{"Номенклатура": item.String(), "Количество": float64(28), "Цена": price}}, true)
				}
				receipt(tc.receiptOrg, 7500, date)
				otherOrg := "A"
				if tc.receiptOrg == "A" {
					otherOrg = ""
				}
				if tc.distractor {
					// The older, cheaper batch must not change either ownership or cost.
					receipt(otherOrg, 100, date.Add(-time.Hour))
				}
				transferDate := date.Add(time.Hour)
				if tc.beforeReceipt {
					transferDate = date.Add(-time.Hour)
				}
				fields := map[string]any{
					"Дата": transferDate, "СкладОтправитель": source.String(), "СкладПолучатель": target.String(), "Организация": orgs[tc.transferOrg],
				}
				goods := []map[string]any{{"Номенклатура": item.String(), "Количество": tc.quantity}}
				id := save("ПеремещениеТоваров", fields, goods, false)
				res, err := svc.Save(ctx, entityservice.SaveRequest{
					Entity: reg.GetEntity("ПеремещениеТоваров"), ID: id, Action: "post", Fields: fields,
					TablePartRows: map[string][]map[string]any{"Товары": goods},
				})
				if err != nil {
					t.Fatal(err)
				}
				if tc.wantFailure {
					if !strings.Contains(res.DSLError, "Недостаточно остатков") {
						t.Fatalf("expected insufficient stock, got %q", res.DSLError)
					}
				} else if res.DSLError != "" {
					t.Fatalf("transfer: %s", res.DSLError)
				}
				stored, err := db.GetByID(ctx, "ПеремещениеТоваров", id, reg.GetEntity("ПеремещениеТоваров"))
				if err != nil {
					t.Fatal(err)
				}
				if posted := asBool(stored["posted"]); posted == tc.wantFailure {
					t.Fatalf("posted=%v, want %v", stored["posted"], !tc.wantFailure)
				}
				key := func(warehouse uuid.UUID, org string) string {
					return fmt.Sprint(warehouse, "/", orgs[org])
				}
				want := map[string][2]float64{key(source, tc.receiptOrg): {28, 210000}}
				if tc.distractor {
					want[key(source, otherOrg)] = [2]float64{28, 2800}
				}
				if !tc.wantFailure {
					want[key(source, tc.receiptOrg)] = [2]float64{21, 157500}
					want[key(target, tc.transferOrg)] = [2]float64{7, 52500}
				}
				for _, name := range []string{"ОстаткиТоваров", "ПартииТоваров"} {
					rows, err := db.Query(ctx, "SELECT склад_id, организация_id, вид_движения, CAST(количество AS NUMERIC), CAST(сумма AS NUMERIC) FROM "+metadata.RegisterTableName(name))
					if err != nil {
						t.Fatal(err)
					}
					got := map[string][2]float64{}
					for rows.Next() {
						var warehouse, direction string
						var org sql.NullString
						var quantity, cost float64
						if err := rows.Scan(&warehouse, &org, &direction, &quantity, &cost); err != nil {
							t.Fatal(err)
						}
						orgValue := any(nil)
						if org.Valid {
							orgValue = org.String
						}
						k := fmt.Sprint(warehouse, "/", orgValue)
						balance := got[k]
						if direction == "Расход" {
							quantity, cost = -quantity, -cost
						}
						got[k] = [2]float64{balance[0] + quantity, balance[1] + cost}
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					if !reflect.DeepEqual(got, want) {
						t.Errorf("%s balances (quantity/cost): got %v, want %v", name, got, want)
					}
				}
			})
		})
	}
}
