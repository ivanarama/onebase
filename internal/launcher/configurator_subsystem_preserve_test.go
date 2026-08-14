package launcher

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"gopkg.in/yaml.v3"
)

// Сохранение подсистемы из конфигуратора обязано быть точечной правкой, а не
// пересборкой файла из struct (#878).
//
// Прежде файл собирался полным yaml.Marshal локальной struct: всё, чего в ней
// нет — незнакомые ключи и любые комментарии, — молча исчезало при первом же
// сохранении. Ровно этот антипаттерн уже дважды чинили: в config/app.yaml
// (#663) и в матрице ролей (#744). Часть данных struct пыталась спасать
// вручную, перенося roles/pages/titles/home_page обратно, — то есть список
// «что не потерять» вели руками, и он неизбежно отставал.
//
// Проверка идёт через публичный хендлер сохранения и смотрит на СЫРОЙ текст
// файла: разбор в структуру показал бы только то, что структура и так знает.
func TestSaveSubsystem_СохраняетКомментарииИНезнакомыеКлючи(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	store := newTestStore(t)
	base := &Base{
		ID:           "subsystem-preserve",
		Name:         "Тест",
		ConfigSource: "database",
		DBType:       "sqlite",
		DBPath:       filepath.Join(t.TempDir(), "config.db"),
	}
	if err := store.Add(base); err != nil {
		t.Fatalf("store.Add: %v", err)
	}

	const original = `# Подсистема отдела продаж — не удалять комментарий
name: АТС
title: АТС
order: 10
# роли этой формой не редактируются
roles: [Диспетчер]
experimental_flag: true
contents:
  catalogs: [ГП_атс]
  pages: [Сводка]
home_page:
  title: Рабочий стол АТС
  widgets:
    - name: СводкаПоАТС
`

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
		{Path: "config/app.yaml", Content: []byte("name: Тест\n")},
		{Path: "catalogs/гп_атс.yaml", Content: []byte("name: ГП_атс\nfields: []\n")},
		{Path: "catalogs/гтп_атс.yaml", Content: []byte("name: ГТП_атс\nfields: []\n")},
		{Path: "subsystems/атс.yaml", Content: []byte(original)},
	}, configdb.VersionOptions{Message: "seed"}); err != nil {
		db.Close()
		t.Fatalf("seed: %v", err)
	}
	db.Close()

	h := &handler{store: store, runner: NewRunner()}
	form := url.Values{
		"subsystem_name": {"АТС"},
		"title":          {"АТС"},
		"order":          {"10"},
		"catalogs":       {"ГП_атс", "ГТП_атс"},
	}
	rec := postCfgRv(t, base.ID, "/bases/"+base.ID+"/configurator/subsystem", form, h.configuratorSaveSubsystem)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}

	db, err = OpenDB(ctx, base)
	if err != nil {
		t.Fatalf("повторный OpenDB: %v", err)
	}
	defer db.Close()
	raw, ok, err := configdb.New(db).ReadFile(ctx, "subsystems/атс.yaml")
	if err != nil || !ok {
		t.Fatalf("ReadFile: ok=%v err=%v", ok, err)
	}
	saved := string(raw)

	for _, must := range []struct{ what, text string }{
		{"комментарий шапки", "# Подсистема отдела продаж"},
		{"комментарий про роли", "# роли этой формой не редактируются"},
		{"незнакомый ключ", "experimental_flag"},
		{"роли", "Диспетчер"},
		{"страницы (форма их не редактирует)", "Сводка"},
		{"заголовок рабочего стола", "Рабочий стол АТС"},
		{"виджет рабочего стола", "СводкаПоАТС"},
	} {
		if !strings.Contains(saved, must.text) {
			t.Errorf("после сохранения потеряно: %s (%q)\n---\n%s", must.what, must.text, saved)
		}
	}
	// И собственно правка применилась.
	if !strings.Contains(saved, "ГТП_атс") {
		t.Errorf("новый состав не сохранён:\n%s", saved)
	}
}

func TestSaveSubsystem_FileModePreservesUntouchedDataAndHomePageSemantics(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	if err := os.MkdirAll(filepath.Join(cfgDir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config", "app.yaml"), []byte("name: Тест\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := writeCfgFileRv(t, cfgDir, "subsystems", "продажи.yaml", `# комментарий подсистемы
name: Продажи
title: Старый
titles:
  en: Sales
icon: cart
order: 1
roles: [Менеджер]
experimental_flag: true
contents:
  catalogs: [Старый]
  pages: [Сводка]
home_page:
  title: Рабочий стол
  titles:
    en: Dashboard
  layout: grid
  widgets:
    - name: СтарыйВиджет
  experimental_home: keep
`)

	form := url.Values{
		"subsystem_name": {"Продажи"},
		"title":          {"Новый заголовок"},
		"order":          {"7"},
		"catalogs":       {"Новый"},
		"home_layout":    {"rows"},
		"home_rows":      {`[["Первый","Второй"]]`},
	}
	rec := postCfgRv(t, "test", "/bases/test/configurator/subsystem", form, h.configuratorSaveSubsystem)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	saved := string(raw)
	for _, fragment := range []string{
		"# комментарий подсистемы",
		"en: Sales",
		"roles: [Менеджер]",
		"experimental_flag: true",
		"pages: [Сводка]",
		"title: Рабочий стол",
		"en: Dashboard",
		"experimental_home: keep",
	} {
		if !strings.Contains(saved, fragment) {
			t.Errorf("после file-mode сохранения потеряно %q:\n%s", fragment, saved)
		}
	}
	var got struct {
		Title    string `yaml:"title"`
		Icon     string `yaml:"icon"`
		Order    int    `yaml:"order"`
		Contents struct {
			Catalogs []string `yaml:"catalogs"`
			Pages    []string `yaml:"pages"`
		} `yaml:"contents"`
		HomePage struct {
			Title  string `yaml:"title"`
			Layout string `yaml:"layout"`
			Rows   []struct {
				Widgets []string `yaml:"widgets"`
			} `yaml:"rows"`
			Widgets []map[string]any `yaml:"widgets"`
		} `yaml:"home_page"`
	}
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("сохранённый YAML не разбирается: %v\n%s", err, saved)
	}
	if got.Title != "Новый заголовок" || got.Icon != "" || got.Order != 7 {
		t.Errorf("поля формы сохранены неверно: title=%q icon=%q order=%d", got.Title, got.Icon, got.Order)
	}
	if len(got.Contents.Catalogs) != 1 || got.Contents.Catalogs[0] != "Новый" ||
		len(got.Contents.Pages) != 1 || got.Contents.Pages[0] != "Сводка" {
		t.Errorf("contents = %+v", got.Contents)
	}
	if got.HomePage.Title != "Рабочий стол" || got.HomePage.Layout != "rows" ||
		len(got.HomePage.Rows) != 1 || len(got.HomePage.Rows[0].Widgets) != 2 || len(got.HomePage.Widgets) != 0 {
		t.Errorf("home_page semantics нарушена: %+v", got.HomePage)
	}
}

func TestSaveSubsystem_MalformedExistingYAMLIsNotOverwritten(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "duplicate keys",
			raw:  "name: Продажи\nname: Дубль\ncontents: {}\n",
		},
		{
			name: "home page is scalar",
			raw:  "name: Продажи\ncontents: {}\nhome_page: сломано\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, cfgDir := newFileBaseHandler(t)
			h.runner = NewRunner()
			path := writeCfgFileRv(t, cfgDir, "subsystems", "продажи.yaml", tt.raw)
			form := url.Values{
				"subsystem_name": {"Продажи"},
				"title":          {"Новый заголовок"},
				"order":          {"2"},
			}
			rec := postCfgRv(t, "test", "/bases/test/configurator/subsystem", form, h.configuratorSaveSubsystem)
			if rec.Code != http.StatusOK {
				t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"ok":false`) {
				t.Fatalf("обработчик не сообщил ошибку: %s", rec.Body.String())
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.raw {
				t.Fatalf("ошибочный YAML был перезаписан:\n--- want\n%s--- got\n%s", tt.raw, got)
			}
		})
	}
}
