//go:build integration

package onebase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/api"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
)

func mustDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return dsn
}

func TestIntegration_FileMode(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()

	db, err := storage.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatalf("auth schema: %v", err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatalf("audit schema: %v", err)
	}

	proj, err := project.Load("examples/minimal")
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	defer proj.Close()

	prepareIntegrationProject(t, ctx, db, proj)

	ts := newIntegrationServer(t, db, authRepo, proj)
	runScenario(t, ts.URL)
}

func TestIntegration_DatabaseMode(t *testing.T) {
	dsn := mustDSN(t)
	ctx := context.Background()

	db, err := storage.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatalf("auth schema: %v", err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatalf("audit schema: %v", err)
	}

	// Scaffold a project into configdb
	cfgRepo := configdb.New(db)
	if err := cfgRepo.EnsureSchema(ctx); err != nil {
		t.Fatalf("configdb schema: %v", err)
	}
	if err := cfgRepo.ImportFromDir(ctx, "examples/minimal"); err != nil {
		t.Fatalf("import config: %v", err)
	}

	proj, err := project.LoadFromDB(ctx, cfgRepo)
	if err != nil {
		t.Fatalf("load from db: %v", err)
	}
	defer proj.Close()

	prepareIntegrationProject(t, ctx, db, proj)

	ts := newIntegrationServer(t, db, authRepo, proj)
	runScenario(t, ts.URL)
}

func prepareIntegrationProject(t *testing.T, ctx context.Context, db *storage.DB, proj *project.Project) {
	t.Helper()
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
		t.Fatalf("migrate registers: %v", err)
	}
	if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
		t.Fatalf("migrate information registers: %v", err)
	}
	if err := db.MigrateConstants(ctx, proj.Constants); err != nil {
		t.Fatalf("migrate constants: %v", err)
	}
	if err := db.EnsureExchangeSchema(ctx); err != nil {
		t.Fatalf("exchange schema: %v", err)
	}
	if err := db.EnsureAccountsTable(ctx); err != nil {
		t.Fatalf("accounts schema: %v", err)
	}
	if err := db.SyncAccounts(ctx, proj.ChartsOfAccounts); err != nil {
		t.Fatalf("sync accounts: %v", err)
	}
	if err := db.MigrateAccountRegisters(ctx, proj.AccountRegisters); err != nil {
		t.Fatalf("migrate account registers: %v", err)
	}
}

func newIntegrationServer(t *testing.T, db *storage.DB, authRepo *auth.Repo, proj *project.Project) *httptest.Server {
	t.Helper()
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities:        proj.Entities,
		Programs:        proj.Programs,
		ManagerPrograms: proj.ManagerPrograms,
		ServicePrograms: proj.ServicePrograms,
		PagePrograms:    proj.PagePrograms,
		Registers:       proj.Registers,
		InfoRegs:        proj.InfoRegisters,
		Enums:           proj.Enums,
		Constants:       proj.Constants,
		Reports:         proj.Reports,
		PrintForms:      proj.PrintForms,
	})
	reg.LoadDSLPrintForms(proj.DSLPrintForms)
	reg.LoadLayoutForms(proj.LayoutForms)
	reg.LoadModules(proj.Modules)
	reg.LoadProcessors(proj.Processors)
	reg.LoadHTTPServices(proj.HTTPServices)
	reg.LoadPages(proj.Pages)
	reg.LoadExchangePlans(proj.ExchangePlans)
	reg.LoadSubsystems(proj.Subsystems)
	reg.LoadJournals(proj.Journals)
	reg.LoadAccountRegisters(proj.AccountRegisters, proj.ChartsOfAccounts)
	reg.LoadWidgets(proj.Widgets)
	reg.LoadHomePage(proj.HomePage)
	if reg.GetProcedure("Списание", "OnPost") == nil {
		t.Fatal("integration project did not load Списание.OnPost")
	}

	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupSiblingProc = reg.GetSiblingProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc
	sched := scheduler.New(db, reg, interp)
	srv := api.New(reg, db, interp, authRepo, "127.0.0.1", 0, ui.Config{}, sched)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("api shutdown: %v", err)
		}
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func runScenario(t *testing.T, baseURL string) {
	t.Helper()

	// 1. Create Counterparty
	body, _ := json.Marshal(map[string]any{"Наименование": "Acme Corp", "ИНН": "1234567890"})
	catalogURL := baseURL + "/catalogs/" + url.PathEscape("Контрагент")
	resp, err := http.Post(catalogURL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create counterparty: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		responseBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create counterparty: status %d: %s", resp.StatusCode, responseBody)
	}
	var cpResult map[string]string
	json.NewDecoder(resp.Body).Decode(&cpResult)
	resp.Body.Close()
	cpID := cpResult["id"]

	// 2. Post a write-off for a unique SKU without stock → the DSL posting
	// hook must reject it even when SQL SUM returns NULL for an empty set.
	missingSKU := "integration-empty-" + uuid.NewString()
	body, _ = json.Marshal(map[string]any{
		"Номер":    "OUT-001",
		"__action": "post",
		"__tableparts": map[string]any{
			"Товары": []map[string]any{{
				"Номенклатура": missingSKU,
				"Количество":   1,
			}},
		},
	})
	writeOffURL := baseURL + "/documents/" + url.PathEscape("Списание")
	resp, err = http.Post(writeOffURL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post invalid write-off: %v", err)
	}
	if resp.StatusCode != http.StatusUnprocessableEntity {
		responseBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("want 422 for write-off without stock, got %d: %s", resp.StatusCode, responseBody)
	}
	resp.Body.Close()

	// 3. Create Invoice with valid Number → expect 201
	body, _ = json.Marshal(map[string]any{"Номер": "INV-001", "Контрагент": cpID})
	documentURL := baseURL + "/documents/" + url.PathEscape("Счёт")
	resp, err = http.Post(documentURL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create valid invoice: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		responseBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("want 201 for valid invoice, got %d: %s", resp.StatusCode, responseBody)
	}
	var invResult map[string]string
	json.NewDecoder(resp.Body).Decode(&invResult)
	resp.Body.Close()
	invID := invResult["id"]
	if invID == "" {
		t.Fatal("expected id in response")
	}

	// 4. GET Invoice by ID
	resp, err = http.Get(documentURL + "/" + invID)
	if err != nil {
		t.Fatalf("get invoice: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get invoice: status %d", resp.StatusCode)
	}
	var fetched map[string]any
	json.NewDecoder(resp.Body).Decode(&fetched)
	resp.Body.Close()
	if fetched["Номер"] != "INV-001" {
		t.Fatalf("fetched Номер mismatch: %v", fetched["Номер"])
	}
}
