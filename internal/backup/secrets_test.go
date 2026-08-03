package backup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/secrets"
	"github.com/ivantit66/onebase/internal/storage"
)

// Копия снимается с базы целиком, поэтому открытый секрет в _settings уедет в
// файл. Отфильтровать его нельзя — значит, о нём надо предупредить.
func TestPlaintextSecretPaths(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	key, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := key.Encrypt("токен")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.SaveLLMConfig(ctx, llm.Config{Endpoints: []llm.Endpoint{
		{Name: "облако", APIKey: "sk-открытым-текстом"},
		{Name: "локально", APIKey: "env:OB_LOCAL"},
		{Name: "шифр", APIKey: encrypted},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveExchangeToken(ctx, "обмен", "токен-открытым-текстом"); err != nil {
		t.Fatal(err)
	}

	got := PlaintextSecretPaths(ctx, db)
	want := map[string]bool{"llm.облако.api_key": true, "exchange.token.обмен": true}
	if len(got) != len(want) {
		t.Fatalf("ожидалось %d открытых секретов, получено %v", len(want), got)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("лишний путь в предупреждении: %s", p)
		}
	}
}

func TestPlaintextSecretPathsCleanBase(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	if err := db.SaveLLMConfig(ctx, llm.Config{Endpoints: []llm.Endpoint{
		{Name: "облако", APIKey: "${env:OB_KEY}"},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := PlaintextSecretPaths(ctx, db); len(got) != 0 {
		t.Fatalf("база без открытых секретов не должна давать предупреждений: %v", got)
	}
	// База без настроек вовсе — тоже тишина.
	empty, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(empty.Close)
	if got := PlaintextSecretPaths(ctx, empty); len(got) != 0 {
		t.Fatalf("пустая база: %v", got)
	}
}
