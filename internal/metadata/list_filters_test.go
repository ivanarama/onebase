package metadata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadListFiltersEntity(t *testing.T, filters string) *Entity {
	t.Helper()
	p := filepath.Join(t.TempDir(), "Заявка.yaml")
	body := "name: Заявка\nfields:\n  - {name: Номер, type: string}\n  - {name: Дата, type: date}\n  - {name: Бренд, type: string}\nlist_filters: " + filters + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := LoadFile(p, KindDocument)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestListFilters_ParsedFromYAML(t *testing.T) {
	e := loadListFiltersEntity(t, "[Номер, \" Дата \"]")
	if strings.Join(e.ListFilters, ",") != "Номер,Дата" {
		t.Fatalf("list_filters = %v", e.ListFilters)
	}
	if err := validateListFilters(e); err != nil {
		t.Fatal(err)
	}
}

// Опечатка в list_filters — ошибка проверки, а не тихо пропавшее поле панели.
func TestListFilters_UnknownOrDuplicateFieldRejected(t *testing.T) {
	if err := validateListFilters(loadListFiltersEntity(t, "[Номер, Бренды]")); err == nil || !strings.Contains(err.Error(), "unknown field Бренды") {
		t.Fatalf("опечатка: %v", err)
	}
	if err := validateListFilters(loadListFiltersEntity(t, "[Номер, Номер]")); err == nil || !strings.Contains(err.Error(), "дважды") {
		t.Fatalf("повтор: %v", err)
	}
}
