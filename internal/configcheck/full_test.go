package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunFullIncludesNameCollisionCheck(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "счёт.yaml"), `name: Счёт
fields:
  - name: Наименование
    type: string
`)
	mkFile(t, filepath.Join(dir, "documents", "счёт.yaml"), `name: Счёт
fields:
  - name: Дата
    type: date
`)

	res := RunFull(dir)

	if res.OK {
		t.Fatalf("RunFull returned OK, want name collision issue")
	}
	for _, is := range res.Issues {
		if is.Kind == "Имя таблицы" && strings.Contains(is.Message, "коллизия имён") {
			if is.Code != CodeNameCollision {
				t.Fatalf("name collision code = %q, want %q", is.Code, CodeNameCollision)
			}
			if is.SuggestedFix == "" {
				t.Fatalf("name collision suggestedFix пустой: %+v", is)
			}
			return
		}
	}
	t.Fatalf("name collision issue not found: %+v", res.Issues)
}
