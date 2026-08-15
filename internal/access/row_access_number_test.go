package access_test

import (
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

func TestDecide_NumberPolicyMatchesLoadedRowsInMemory(t *testing.T) {
	entity := &metadata.Entity{
		Name: "Invoice",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Amount", Type: metadata.FieldTypeNumber, Length: 5, Scale: 2},
			{Name: "Code", Type: metadata.FieldTypeString},
		},
	}
	storedDecimal := decimal.RequireFromString("100.00")

	tests := []struct {
		name   string
		field  string
		op     string
		value  auth.RowValue
		actual any
		want   bool
	}{
		{name: "eq canonicalizes quoted number", field: "Amount", op: "eq", value: auth.RowValue{Literal: "100"}, actual: "100.00", want: true},
		{name: "eq canonicalizes driver decimal", field: "Amount", op: "eq", value: auth.RowValue{Literal: "100.0"}, actual: storedDecimal, want: true},
		{name: "eq canonicalizes PostgreSQL numeric", field: "Amount", op: "eq", value: auth.RowValue{Literal: "100"}, actual: pgtype.Numeric{Int: big.NewInt(10000), Exp: -2, Valid: true}, want: true},
		{name: "eq canonicalizes byte text", field: "Amount", op: "eq", value: auth.RowValue{Literal: "100"}, actual: []byte("100.00"), want: true},
		{name: "eq uses declared scale rounding", field: "Amount", op: "eq", value: auth.RowValue{Literal: "100.004"}, actual: "100.00", want: true},
		{name: "ne cannot admit an equal quoted number", field: "Amount", op: "ne", value: auth.RowValue{Literal: "100"}, actual: "100.00", want: false},
		{name: "in canonicalizes each quoted number", field: "Amount", op: "in", value: auth.RowValue{List: []any{"99", "100.0"}}, actual: "100.00", want: true},
		{name: "not_in cannot admit an equal quoted number", field: "Amount", op: "not_in", value: auth.RowValue{List: []any{"99", "100"}}, actual: "100.00", want: false},
		{name: "synthetic version is numeric", field: "_version", op: "eq", value: auth.RowValue{Literal: "7"}, actual: int64(7), want: true},
		{name: "string leading zeroes stay significant", field: "Code", op: "eq", value: auth.RowValue{Literal: "1"}, actual: "001", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policies := auth.RowPolicies{"read": {
				Field: tt.field,
				Op:    tt.op,
				Value: tt.value,
			}}
			user := &auth.User{Roles: []*auth.Role{docRole(entity.Name, []string{"read"}, policies)}}
			decision, err := access.Decide(user, "document", entity.Name, "read", entity)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if got := storage.MatchPredicate(map[string]any{tt.field: tt.actual}, decision.Predicate); got != tt.want {
				t.Fatalf("MatchPredicate = %v, want %v; predicate=%+v", got, tt.want, decision.Predicate)
			}
		})
	}
}

func TestDecide_InvalidNumberPolicyFailsClosed(t *testing.T) {
	entity := &metadata.Entity{
		Name:   "Invoice",
		Kind:   metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Amount", Type: metadata.FieldTypeNumber, Length: 5, Scale: 2}},
	}
	tests := []struct {
		name   string
		policy auth.RowPolicy
	}{
		{name: "eq", policy: auth.RowPolicy{Field: "Amount", Op: "eq", Value: auth.RowValue{Literal: "not-a-number"}}},
		{name: "ne", policy: auth.RowPolicy{Field: "Amount", Op: "ne", Value: auth.RowValue{Literal: "not-a-number"}}},
		{name: "in", policy: auth.RowPolicy{Field: "Amount", Op: "in", Value: auth.RowValue{List: []any{"100", "not-a-number"}}}},
		{name: "not_in literal list", policy: auth.RowPolicy{Field: "Amount", Op: "not_in", Value: auth.RowValue{Literal: []any{"100", "not-a-number"}}}},
		{name: "any sibling cannot bypass invalid leaf", policy: auth.RowPolicy{Any: []auth.RowPolicy{
			{Field: "Amount", Op: "ne", Value: auth.RowValue{Literal: "not-a-number"}},
			{Field: "Amount", Op: "eq", Value: auth.RowValue{Literal: "100"}},
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policies := auth.RowPolicies{"read": tt.policy}
			user := &auth.User{Roles: []*auth.Role{docRole(entity.Name, []string{"read"}, policies)}}
			if decision, err := access.Decide(user, "document", entity.Name, "read", entity); err == nil {
				t.Fatalf("Decide = %+v, want malformed NUMBER policy error", decision)
			}
		})
	}
}

func TestDecide_RegisterLineNumberPolicyMatchesLoadedRowsInMemory(t *testing.T) {
	entity := storage.RegisterPredicateEntity(&metadata.Register{Name: "Ledger"})
	policies := auth.RowPolicies{"read": {
		Field: "line_number",
		Op:    "ne",
		Value: auth.RowValue{Literal: "7"},
	}}
	role := &auth.Role{Permissions: auth.Permission{
		Registers: map[string][]string{entity.Name: {"read"}},
		RowAccess: auth.RowAccess{Registers: map[string]auth.RowPolicies{
			entity.Name: policies,
		}},
	}}
	decision, err := access.Decide(&auth.User{Roles: []*auth.Role{role}}, "register", entity.Name, "read", entity)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if storage.MatchPredicate(map[string]any{"line_number": int64(7)}, decision.Predicate) {
		t.Fatal("line_number ne policy admitted an equal numeric value")
	}
}

func TestDecide_ReferenceNumberPolicyMatchesLoadedRowsInMemory(t *testing.T) {
	client := &metadata.Entity{
		Name:   "Client",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "CreditLimit", Type: metadata.FieldTypeNumber, Length: 7, Scale: 2}},
	}
	order := &metadata.Entity{
		Name:   "Order",
		Kind:   metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Client", Type: metadata.FieldTypeString, RefEntity: client.Name}},
	}
	policies := auth.RowPolicies{"read": {
		Field: "Client.CreditLimit",
		Op:    "ne",
		Value: auth.RowValue{Literal: "100"},
	}}
	user := &auth.User{Roles: []*auth.Role{docRole(order.Name, []string{"read"}, policies)}}
	decision, err := access.DecideWithLookup(user, "document", order.Name, "read", order, testEntityLookup{client.Name: client})
	if err != nil {
		t.Fatalf("DecideWithLookup: %v", err)
	}
	clientID := uuid.New()
	allowed := storage.MatchPredicateWithRefs(map[string]any{"Client": clientID}, decision.Predicate,
		func(entity *metadata.Entity, id uuid.UUID) (map[string]any, bool) {
			if entity != client || id != clientID {
				return nil, false
			}
			return map[string]any{"CreditLimit": "100.00"}, true
		})
	if allowed {
		t.Fatal("reference ne policy admitted a row whose numeric value is equal after canonicalization")
	}
}
