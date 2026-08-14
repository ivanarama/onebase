package launcher

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
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
