package launcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"gopkg.in/yaml.v3"
)

// Состав подсистемы в database-режиме должен сохраняться в _onebase_config:
// именно оттуда конфигуратор и рантайм перечитывают конфигурацию после POST.
func TestSaveSubsystem_DBModePersistsContents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	store := newTestStore(t)
	base := &Base{
		ID:           "subsystem-db-test",
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
		{Path: "config/app.yaml", Content: []byte("name: Тест\n")},
		{Path: "catalogs/гп_атс.yaml", Content: []byte("name: ГП_атс\nfields: []\n")},
		{Path: "catalogs/гтп_атс.yaml", Content: []byte("name: ГТП_атс\nfields: []\n")},
		{Path: "catalogs/сайт_атс.yaml", Content: []byte("name: сайт_атс\nfields: []\n")},
		{
			Path: "subsystems/атс.yaml",
			Content: []byte(
				"defaults: &defaults\n" +
					"  icon: cart\n" +
					"name: АТС\n" +
					"title: АТС\n" +
					"roles: [Диспетчер]\n" +
					"order: 10\n" +
					"<<: *defaults\n" +
					"contents:\n" +
					"  catalogs: [ГП_атс]\n",
			),
		},
	}, configdb.VersionOptions{Message: "seed subsystem"}); err != nil {
		db.Close()
		t.Fatalf("seed configdb: %v", err)
	}
	db.Close()

	h := &handler{store: store, runner: NewRunner()}
	form := url.Values{
		"subsystem_name": {"АТС"},
		"title":          {"АТС"},
		"order":          {"10"},
		"catalogs":       {"ГП_атс", "ГТП_атс", "сайт_атс"},
	}
	rec := postCfgRv(
		t,
		base.ID,
		"/bases/"+base.ID+"/configurator/subsystem",
		form,
		h.configuratorSaveSubsystem,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, rec.Body.String())
	}
	if !response.OK {
		t.Fatalf("конфигуратор не перечитал сохранённую конфигурацию: %s", response.Error)
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
	var saved struct {
		Icon     string   `yaml:"icon"`
		Roles    []string `yaml:"roles"`
		Contents struct {
			Catalogs []string `yaml:"catalogs"`
		} `yaml:"contents"`
	}
	if err := yaml.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("сохранённый YAML не разбирается: %v\n%s", err, raw)
	}
	wantCatalogs := []string{"ГП_атс", "ГТП_атс", "сайт_атс"}
	if !slices.Equal(saved.Contents.Catalogs, wantCatalogs) {
		t.Fatalf("catalogs = %v, ожидалось %v", saved.Contents.Catalogs, wantCatalogs)
	}
	if !slices.Equal(saved.Roles, []string{"Диспетчер"}) {
		t.Fatalf("скрытый от формы whitelist roles потерян: %v", saved.Roles)
	}
	if saved.Icon != "" {
		t.Fatalf("очищенное поле icon восстановилось из YAML merge: %q\n%s", saved.Icon, raw)
	}
}
