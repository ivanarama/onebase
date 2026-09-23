package ui

import (
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestNewOfflineServerLoadsExchangePlans(t *testing.T) {
	ctx := t.Context()
	proj, err := project.Load(filepath.Join("..", "..", "examples", "cms"))
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	t.Cleanup(proj.Close)

	plan := &metadata.ExchangePlan{Name: "Offline"}
	proj.ExchangePlans = []*metadata.ExchangePlan{plan}

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "offline-exchange.db"))
	if err != nil {
		t.Fatalf("connect SQLite: %v", err)
	}
	t.Cleanup(db.Close)

	_, reg, err := NewOfflineServer(proj, db)
	if err != nil {
		t.Fatalf("NewOfflineServer: %v", err)
	}
	if got := reg.GetExchangePlan("offline"); got != plan {
		t.Fatalf("exchange plan was not loaded: got %p, want %p", got, plan)
	}
}
