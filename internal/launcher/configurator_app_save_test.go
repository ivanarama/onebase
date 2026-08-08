package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/project"
	"gopkg.in/yaml.v3"
)

// Образец app.yaml: ключи именно те, что понимает project.AppConfig — загрузчик
// читает с KnownFields(true), поэтому выдуманные ключи не прошли бы проверку
// «файл после сохранения всё ещё разбирается».
const appYAMLWithForeignBlocks = `# Конфигурация торговли
name: Торговля
version: "1.0"
support: help@example.org
email:
  smtp_host: smtp.example.org
  smtp_port: 587
  from_address: robot@example.org
attachments:
  max_file_size_mb: 20
backup:
  enabled: true
  schedule: "0 2 * * *"
  keep_last: 7
  s3:
    endpoint: minio.local:9000
    bucket: onebase
    access_key: ${env:S3_KEY}
`

// postCfgMultipart POST-ит форму как multipart/form-data: configuratorSaveApp
// разбирает тело через ParseMultipartForm и на urlencoded вернёт ErrNotMultipart.
func postCfgMultipart(t *testing.T, id, path string, form url.Values, fn http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	body := new(bytes.Buffer)
	mw := multipart.NewWriter(body)
	for k, vs := range form {
		for _, v := range vs {
			if err := mw.WriteField(k, v); err != nil {
				t.Fatalf("multipart WriteField(%s): %v", k, err)
			}
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart Close: %v", err)
	}
	req := httptest.NewRequest("POST", path, body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Onebase-Ajax", "1")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

// cfgResponse разбирает JSON-ответ renderCfg (ajax-ветка).
func cfgResponse(t *testing.T, rec *httptest.ResponseRecorder) (ok bool, errText string) {
	t.Helper()
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("разбор ответа %q: %v", rec.Body.String(), err)
	}
	return resp.OK, resp.Error
}

// assertAppConfigLoads проверяет, что записанное всё ещё разбирается в
// project.AppConfig на тех же условиях, что и у загрузчика платформы:
// project.LoadConfig декодирует с KnownFields(true), поэтому лишний или
// поехавший ключ он отвергает.
func assertAppConfigLoads(t *testing.T, raw []byte) {
	t.Helper()
	var cfg project.AppConfig
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("записанный app.yaml не разбирается: %v\nсодержимое:\n%s", err, raw)
	}
}

// Форма «Свойства конфигурации» правит восемь верхнеуровневых ключей. Всё
// остальное — email, attachments, backup вместе с ключами доступа S3 — обязано
// пережить сохранение (issue #656). Раньше файл собирался заново из структуры
// на восемь полей, и после смены версии от app.yaml оставалось два ключа.
func TestConfiguratorSaveApp_KeepsForeignBlocks(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	appPath := writeCfgFile(t, cfgDir, "config", "app.yaml", appYAMLWithForeignBlocks)

	rec := postCfgMultipart(t, "test", "/bases/test/configurator/app", url.Values{
		"app_name":    {"Торговля"},
		"app_version": {"1.1"},
		"app_support": {"help@example.org"},
	}, h.configuratorSaveApp)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	raw, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("чтение app.yaml: %v", err)
	}
	assertAppConfigLoads(t, raw)
	assertFileContains(t, appPath,
		`version: "1.1"`, // правка формы применилась
		"smtp_host: smtp.example.org",
		"max_file_size_mb: 20",
		"${env:S3_KEY}",           // ключ доступа S3 не потерян
		`schedule: "0 2 * * *"`,   // блок backup цел, вместе с кавычками
		"# Конфигурация торговли", // комментарии сохраняются
	)
}

// Тот же сценарий в режиме config_source: database — файл лежит в _onebase_config,
// а не на диске, и ветка записи там своя.
func TestConfiguratorSaveApp_DBModeKeepsForeignBlocks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	base := &Base{
		ID:           "app-db-test",
		Name:         "ТестБД",
		ConfigSource: "database",
		DBType:       "sqlite",
		DBPath:       filepath.Join(t.TempDir(), "config.db"),
	}
	if err := store.Add(base); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	ctx := context.Background()
	db, err := OpenDB(ctx, base)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		db.Close()
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := repo.SaveFiles(ctx, []configdb.ConfigFile{
		{Path: "config/app.yaml", Content: []byte(appYAMLWithForeignBlocks)},
	}, configdb.VersionOptions{Message: "seed app.yaml"}); err != nil {
		db.Close()
		t.Fatalf("seed configdb: %v", err)
	}
	db.Close()

	h := &handler{store: store, runner: NewRunner()}
	rec := postCfgMultipart(t, base.ID, "/bases/"+base.ID+"/configurator/app", url.Values{
		"app_name":    {"Торговля"},
		"app_version": {"1.1"},
	}, h.configuratorSaveApp)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	db, err = OpenDB(ctx, base)
	if err != nil {
		t.Fatalf("OpenDB после сохранения: %v", err)
	}
	defer db.Close()
	raw, found, err := configdb.New(db).ReadFile(ctx, "config/app.yaml")
	if err != nil || !found {
		t.Fatalf("ReadFile: found=%v err=%v", found, err)
	}
	assertAppConfigLoads(t, raw)
	for _, must := range []string{`version: "1.1"`, "smtp_host: smtp.example.org", "${env:S3_KEY}"} {
		if !strings.Contains(string(raw), must) {
			t.Errorf("в app.yaml нет фрагмента %q\nполучилось:\n%s", must, raw)
		}
	}
}

// Очищенное поле формы удаляет ключ — то же, что раньше давал omitempty. Прочие
// блоки при этом не трогаются.
func TestConfiguratorSaveApp_ClearedFieldRemovesKey(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	appPath := writeCfgFile(t, cfgDir, "config", "app.yaml", appYAMLWithForeignBlocks)

	rec := postCfgMultipart(t, "test", "/bases/test/configurator/app", url.Values{
		"app_name":    {"Торговля"},
		"app_version": {"1.0"},
		"app_support": {""},
	}, h.configuratorSaveApp)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}
	raw, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("чтение app.yaml: %v", err)
	}
	if strings.Contains(string(raw), "support:") {
		t.Errorf("очищенное поле не удалило ключ support:\n%s", raw)
	}
	assertFileContains(t, appPath, "smtp_host: smtp.example.org")
}

// Файла ещё нет — сохранение создаёт его с нуля (первое заполнение свойств).
func TestConfiguratorSaveApp_CreatesFileWhenAbsent(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()

	rec := postCfgMultipart(t, "test", "/bases/test/configurator/app", url.Values{
		"app_name":    {"Новая"},
		"app_version": {"1.0"},
	}, h.configuratorSaveApp)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}
	assertFileContains(t, filepath.Join(cfgDir, "config", "app.yaml"), "name: Новая", `version: "1.0"`)
}

// Битый app.yaml — отказ с видимой ошибкой, а не тихая перезапись: перезапись и
// есть механизм потери данных. Файл остаётся нетронутым, починить можно на
// вкладке «Файлы».
func TestConfiguratorSaveApp_BrokenYAMLRefusesInsteadOfOverwrite(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	const broken = "name: Торговля\n  version: [сломано\n"
	appPath := writeCfgFile(t, cfgDir, "config", "app.yaml", broken)

	rec := postCfgMultipart(t, "test", "/bases/test/configurator/app", url.Values{
		"app_name":    {"Торговля"},
		"app_version": {"1.1"},
	}, h.configuratorSaveApp)
	if ok, _ := cfgResponse(t, rec); ok {
		t.Fatalf("сохранение поверх битого app.yaml прошло успешно")
	}
	raw, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("чтение app.yaml: %v", err)
	}
	if string(raw) != broken {
		t.Errorf("битый app.yaml изменён:\n%s", raw)
	}
}

// Настройки бэкапа правят только блок backup. Раньше форма писала name (беря
// его из реестра лаунчера!) и backup, стирая версию, email, llm и сам backup.s3
// с ключами доступа.
func TestBackupSettings_KeepsAppConfig(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	appPath := writeCfgFile(t, cfgDir, "config", "app.yaml", appYAMLWithForeignBlocks)

	rec := postCfg(t, "test", "/bases/test/configurator/backup-settings", url.Values{
		"backup_enabled":  {"on"},
		"backup_schedule": {"0 3 * * *"},
		"backup_keep":     {"5"},
	}, h.backupSettings)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	raw, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("чтение app.yaml: %v", err)
	}
	assertAppConfigLoads(t, raw)
	assertFileContains(t, appPath,
		"name: Торговля", // имя конфигурации не подменено именем базы
		`version: "1.0"`, // прочие ключи целы
		"smtp_host: smtp.example.org",
		"${env:S3_KEY}", // ключ доступа S3 не потерян
		"schedule: 0 3 * * *",
		"keep_last: 5",
	)
}
