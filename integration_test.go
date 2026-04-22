//go:build integration

package onebase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ivantit66/onebase/internal/api"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestIntegration_Scenario12(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	db, err := storage.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	proj, err := project.Load("examples/simple-erp")
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	reg := runtime.NewRegistry()
	reg.Load(proj.Entities, proj.Programs)
	interp := interpreter.New()
	srv := api.New(reg, db, interp)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Create Counterparty
	body, _ := json.Marshal(map[string]any{"Name": "Acme Corp", "INN": "1234567890"})
	resp, err := http.Post(ts.URL+"/catalogs/counterparty", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create counterparty: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create counterparty: status %d", resp.StatusCode)
	}
	var cpResult map[string]string
	json.NewDecoder(resp.Body).Decode(&cpResult)
	resp.Body.Close()
	cpID := cpResult["id"]

	// 2. Create Invoice with empty Number → expect 422
	body, _ = json.Marshal(map[string]any{"Number": "", "Counterparty": cpID})
	resp, err = http.Post(ts.URL+"/documents/invoice", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create invalid invoice: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 for empty number, got %d", resp.StatusCode)
	}

	// 3. Create Invoice with valid Number → expect 200
	body, _ = json.Marshal(map[string]any{"Number": "INV-001", "Counterparty": cpID})
	resp, err = http.Post(ts.URL+"/documents/invoice", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create valid invoice: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 for valid invoice, got %d", resp.StatusCode)
	}
	var invResult map[string]string
	json.NewDecoder(resp.Body).Decode(&invResult)
	resp.Body.Close()
	invID := invResult["id"]
	if invID == "" {
		t.Fatal("expected id in response")
	}

	// 4. GET Invoice by ID
	resp, err = http.Get(ts.URL + "/documents/invoice/" + invID)
	if err != nil {
		t.Fatalf("get invoice: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get invoice: status %d", resp.StatusCode)
	}
	var fetched map[string]any
	json.NewDecoder(resp.Body).Decode(&fetched)
	resp.Body.Close()
	if fetched["Number"] != "INV-001" {
		t.Fatalf("fetched Number mismatch: %v", fetched["Number"])
	}
}
