package page

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("панель.yaml", "name: Панель\ntitle: Панель руководителя\ntitles:\n  en: Manager\nroles: [Руководитель]\nparams: [период]\n")
	write("без_имени.yaml", "title: Без имени\n")
	write("ignore.txt", "не yaml")

	pages, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("ожидалось 2 страницы, получено %d", len(pages))
	}

	var panel, noname *Page
	for _, p := range pages {
		switch p.Name {
		case "Панель":
			panel = p
		case "без_имени":
			noname = p
		}
	}
	if panel == nil {
		t.Fatal("страница «Панель» не загружена")
		return
	}
	if got := panel.DisplayName("en"); got != "Manager" {
		t.Errorf("DisplayName(en) = %q", got)
	}
	if got := panel.DisplayName("ru"); got != "Панель руководителя" {
		t.Errorf("DisplayName(ru) = %q", got)
	}
	if len(panel.Roles) != 1 || panel.Roles[0] != "Руководитель" {
		t.Errorf("Roles = %v", panel.Roles)
	}
	if len(panel.Params) != 1 || panel.Params[0] != "период" {
		t.Errorf("Params = %v", panel.Params)
	}
	// Имя берётся из имени файла, заголовок — из YAML.
	if noname == nil {
		t.Fatal("страница без name не получила имя из файла")
		return
	}
	if noname.Title != "Без имени" {
		t.Errorf("noname.Title = %q", noname.Title)
	}
}

func TestLoadDir_MissingDir(t *testing.T) {
	pages, err := LoadDir(filepath.Join(t.TempDir(), "нет-такой"))
	if err != nil {
		t.Fatalf("отсутствующая папка не должна быть ошибкой: %v", err)
	}
	if pages != nil {
		t.Errorf("ожидался nil, получено %v", pages)
	}
}

// Marshal опускает пустые необязательные поля (omitempty), но всегда пишет name.
func TestMarshal_OmitsEmptyFields(t *testing.T) {
	out, err := Marshal(&Page{Name: "Сводка"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if want := "name: Сводка\n"; got != want {
		t.Errorf("Marshal минимальной страницы = %q, ожидалось %q", got, want)
	}
}

// SaveFile → LoadFile сохраняет все заполненные поля (round-trip) и создаёт
// каталог. Это путь, которым конфигуратор пишет pages/<имя>.yaml в файловом
// режиме (план 66, доработка 2).
func TestSaveFile_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pages", "Панель.yaml")
	in := &Page{
		Name:   "Панель",
		Title:  "Панель руководителя",
		Titles: map[string]string{"en": "Manager dashboard"},
		Icon:   "layout-dashboard",
		Roles:  []string{"Руководитель", "Бухгалтер"},
		Params: []string{"период"},
	}
	if err := SaveFile(path, in); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got.Name != in.Name || got.Title != in.Title || got.Icon != in.Icon {
		t.Errorf("round-trip потерял скаляры: %+v", got)
	}
	if got.Titles["en"] != "Manager dashboard" {
		t.Errorf("Titles = %v", got.Titles)
	}
	if len(got.Roles) != 2 || got.Roles[0] != "Руководитель" {
		t.Errorf("Roles = %v", got.Roles)
	}
	if len(got.Params) != 1 || got.Params[0] != "период" {
		t.Errorf("Params = %v", got.Params)
	}
}
