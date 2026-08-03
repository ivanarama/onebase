package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// Ключ ИИ, вынесенный в ${env:...}, не должен попадать в базу значением.
//
// Регресс на дыру, которую закрывает план 83: app.yaml раскрывался при загрузке,
// и applyAppAISettings клал в _settings.llm.config уже РАСКРЫТЫЙ ключ — откуда
// он уезжал в обычный дамп бэкапа (pg_dump / VACUUM INTO копируют базу целиком)
// открытым текстом. То есть аккуратно вынесенный в окружение секрет всё равно
// оказывался в резервной копии.
func TestApplyAppAISettingsKeepsSecretReference(t *testing.T) {
	const key = "sk-очень-секретный-ключ"
	t.Setenv("ONEBASE_TEST_AI_KEY", key)

	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	appYAML := `name: Demo
llm:
  enabled: true
  endpoints:
    - name: z_ai
      kind: anthropic
      api_key: "${env:ONEBASE_TEST_AI_KEY}"
  models:
    - {name: glm-4.6, endpoint: z_ai}
  profiles:
    - {task: чат, models: [glm-4.6]}
`
	if err := os.WriteFile(filepath.Join(cfgDir, "app.yaml"), []byte(appYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	appCfg, err := project.LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(dir, "ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	if errs := applyAppAISettings(ctx, db, appCfg); len(errs) != 0 {
		t.Fatalf("applyAppAISettings: %v", errs)
	}

	var raw string
	if err := db.QueryRow(ctx, `SELECT value FROM _settings WHERE key = 'llm.config'`).Scan(&raw); err != nil {
		t.Fatalf("чтение llm.config: %v", err)
	}
	if strings.Contains(raw, key) {
		t.Fatalf("ключ ИИ сохранён в базу открытым текстом: %s", raw)
	}
	if !strings.Contains(raw, "${env:ONEBASE_TEST_AI_KEY}") {
		t.Fatalf("в базе должна остаться ссылка на секрет, получено: %s", raw)
	}

	// При этом ИИ работоспособен: значение подставляется в момент вызова.
	stored, err := db.GetLLMConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := stored.Resolve("чат")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := resolved[0].Endpoint.APIKey; got != key {
		t.Fatalf("ключ не разыменован при использовании: %q", got)
	}
}
