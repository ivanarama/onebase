package storage_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestCreateTableSQL_Counterparty(t *testing.T) {
	e := &metadata.Entity{
		Name: "Counterparty",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString},
			{Name: "INN", Type: metadata.FieldTypeString},
		},
	}
	sql := storage.CreateTableSQL(e)
	if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS counterparty") {
		t.Fatalf("missing table name: %s", sql)
	}
	if !strings.Contains(sql, "id UUID PRIMARY KEY") {
		t.Fatalf("missing id column: %s", sql)
	}
	if !strings.Contains(sql, "name TEXT") {
		t.Fatalf("missing name column: %s", sql)
	}
	if !strings.Contains(sql, "inn TEXT") {
		t.Fatalf("missing inn column: %s", sql)
	}
}

func TestCreateTableSQL_Invoice(t *testing.T) {
	e := &metadata.Entity{
		Name: "Invoice",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Number", Type: metadata.FieldTypeString},
			{Name: "Date", Type: metadata.FieldTypeDate},
			{Name: "Counterparty", Type: "reference:Counterparty", RefEntity: "Counterparty"},
		},
	}
	sql := storage.CreateTableSQL(e)
	if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS invoice") {
		t.Fatalf("missing table: %s", sql)
	}
	if !strings.Contains(sql, "date TIMESTAMPTZ") {
		t.Fatalf("missing date column: %s", sql)
	}
	if !strings.Contains(sql, "counterparty_id UUID") {
		t.Fatalf("missing reference column: %s", sql)
	}
	if !strings.Contains(sql, "REFERENCES counterparty(id)") {
		t.Fatalf("missing FK: %s", sql)
	}
}
