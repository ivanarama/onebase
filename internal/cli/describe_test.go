package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDescribe_V2Contract(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// item_form с расширенной записью — describe обязан отдавать и состав
	// формы, и признак «только просмотр» (#1011): по этому контракту работают
	// ИИ-помощник и внешние инструменты.
	mustWrite("catalogs/клиент.yaml", "name: Клиент\nfields:\n  - {name: Наименование, type: string}\n  - {name: ТелефоныНорм, type: string}\nitem_form:\n  - Наименование\n  - name: ТелефоныНорм\n    readonly: true\n")
	mustWrite("documents/заказ.yaml", "name: Заказ\nposting: true\nfields:\n  - {name: Клиент, type: reference:Клиент}\ntableparts:\n  - name: Товары\n    fields:\n      - {name: Количество, type: number}\n")
	mustWrite("reports/продажи.yaml", "name: Продажи\nquery: \"ВЫБРАТЬ 1 КАК Сумма\"\nparams:\n  - {name: Период, type: date}\n")
	mustWrite("widgets/продажи.yaml", "name: Продажи\ntype: chart\nquery: \"ВЫБРАТЬ 1 КАК Период, 2 КАК Сумма\"\nchart_kind: line\nx_field: Период\ny_fields: [Сумма]\n")
	mustWrite("roles/basic.yaml", "name: Оператор\npermissions:\n  catalogs:\n    Клиент: [read, write]\n")
	mustWrite("src/заказ.posting.os", "Процедура Проведение() Экспорт\nКонецПроцедуры\n")

	cmd := describeCmd
	if err := cmd.Flags().Set("project", dir); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("id", ""); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("sqlite", ""); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("db", ""); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	done := make(chan error, 1)
	go func() {
		_, err := out.ReadFrom(r)
		done <- err
	}()
	if err := runDescribe(cmd, nil); err != nil {
		_ = w.Close()
		t.Fatalf("runDescribe: %v", err)
	}
	_ = w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	var got struct {
		SchemaVersion int `json:"schemaVersion"`
		Catalogs      []struct {
			Name             string   `json:"name"`
			ItemForm         []string `json:"itemForm"`
			ItemFormReadonly []string `json:"itemFormReadonly"`
		} `json:"catalogs"`
		Reports []struct {
			Name   string `json:"name"`
			Query  string `json:"query"`
			Source struct {
				File string `json:"file"`
			} `json:"source"`
		} `json:"reports"`
		Widgets []struct {
			Name      string   `json:"name"`
			Type      string   `json:"type"`
			ChartKind string   `json:"chartKind"`
			YFields   []string `json:"yFields"`
		} `json:"widgets"`
		Builtins []struct {
			Name      string `json:"name"`
			Signature string `json:"signature"`
		} `json:"builtins"`
		Roles []struct {
			Name        string `json:"name"`
			Permissions struct {
				Catalogs map[string][]string `json:"catalogs"`
			} `json:"permissions"`
			Source struct {
				File string `json:"file"`
			} `json:"source"`
		} `json:"roles"`
		Modules []struct {
			Name   string `json:"name"`
			Source struct {
				File string `json:"file"`
			} `json:"source"`
			Procedures []struct {
				Name   string `json:"name"`
				Export bool   `json:"export"`
				Source struct {
					File string `json:"file"`
				} `json:"source"`
			} `json:"procedures"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("describe не JSON: %v\n%s", err, out.String())
	}
	if got.SchemaVersion != 2 {
		t.Fatalf("schemaVersion=%d, want 2", got.SchemaVersion)
	}
	if len(got.Catalogs) != 1 || strings.Join(got.Catalogs[0].ItemForm, ",") != "Наименование,ТелефоныНорм" {
		t.Fatalf("состав формы элемента не раскрыт: %+v", got.Catalogs)
	}
	if strings.Join(got.Catalogs[0].ItemFormReadonly, ",") != "ТелефоныНорм" {
		t.Fatalf("признак «только просмотр» не раскрыт: %+v", got.Catalogs[0])
	}
	if len(got.Reports) != 1 || got.Reports[0].Query == "" || got.Reports[0].Source.File != "reports/продажи.yaml" {
		t.Fatalf("reports не раскрыты: %+v", got.Reports)
	}
	if len(got.Widgets) != 1 || got.Widgets[0].Type != "chart" || got.Widgets[0].ChartKind != "line" || len(got.Widgets[0].YFields) != 1 {
		t.Fatalf("widgets не раскрыты: %+v", got.Widgets)
	}
	var hasStrReplace bool
	for _, b := range got.Builtins {
		if strings.EqualFold(b.Name, "стрзаменить") && strings.Contains(b.Signature, "СтрЗаменить(") {
			hasStrReplace = true
			break
		}
	}
	if !hasStrReplace {
		t.Fatal("builtins не содержат langref-сигнатуру СтрЗаменить")
	}
	if len(got.Roles) != 1 || got.Roles[0].Name != "Оператор" || got.Roles[0].Source.File != "roles/basic.yaml" {
		t.Fatalf("roles не раскрыты с source: %+v", got.Roles)
	}
	if perms := got.Roles[0].Permissions.Catalogs["Клиент"]; len(perms) != 2 || perms[0] != "read" || perms[1] != "write" {
		t.Fatalf("permissions роли не раскрыты: %+v", got.Roles[0].Permissions)
	}
	var hasExportProc bool
	for _, m := range got.Modules {
		for _, p := range m.Procedures {
			if p.Name == "Проведение" && p.Export {
				if p.Source.File != "src/заказ.posting.os" {
					t.Fatalf("source процедуры должен быть относительным к проекту, got %q", p.Source.File)
				}
				hasExportProc = true
			}
		}
	}
	if !hasExportProc {
		t.Fatalf("процедуры модулей не раскрыты с export-флагом: %+v", got.Modules)
	}
}
