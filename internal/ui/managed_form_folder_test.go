package ui

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// #618: кнопка «📁 Группа» управляемой формы должна создавать группу. Автоформа
// рисует is_folder/parent_id полями, managed — нет, поэтому признак группы терялся
// при создании (восстанавливать неоткуда — записи в БД ещё нет). Проверяем и
// перенос query→data (hierarchyCreateHints), и отрисовку скрытых полей шаблоном.

func TestHierarchyCreateHints(t *testing.T) {
	ent := &metadata.Entity{Name: "Папки", Kind: metadata.KindCatalog, Hierarchical: true}
	flat := &metadata.Entity{Name: "Плоский", Kind: metadata.KindCatalog}

	cases := []struct {
		name       string
		entity     *metadata.Entity
		query      string
		isNew      bool
		wantFolder bool
		wantParent string
	}{
		{"группа", ent, "is_folder=true", true, true, ""},
		{"в группе", ent, "parent_id=P1", true, false, "P1"},
		{"группа в группе", ent, "is_folder=true&parent_id=P1", true, true, "P1"},
		{"обычный элемент", ent, "", true, false, ""},
		{"существующая запись не подхватывает query", ent, "is_folder=true", false, false, ""},
		{"неиерархический игнорирует", flat, "is_folder=true", true, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/ui/catalog/X/new?"+c.query, nil)
			gotFolder, gotParent := hierarchyCreateHints(r, c.entity, c.isNew)
			if gotFolder != c.wantFolder || gotParent != c.wantParent {
				t.Errorf("hierarchyCreateHints = (%v, %q), ожидалось (%v, %q)",
					gotFolder, gotParent, c.wantFolder, c.wantParent)
			}
		})
	}
}

// Шаблон page-managed-form при NewIsFolder/NewParentID рисует скрытые поля,
// которые Upsert прочитает как признак группы и родителя.
func TestManagedFormRendersFolderHiddenFields(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Папки", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	form := &metadata.FormModule{Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name, LayoutKind: metadata.FormLayoutManaged}

	render := func(data map[string]any) string {
		base := map[string]any{
			"Entity": ent, "Form": form, "IsNew": true,
			"Values": map[string]string{}, "RefOptions": map[string]any{},
			"EnumOptions": map[string]any{}, "TablePartRows": map[string][]map[string]any{},
			"Lang": "ru",
		}
		for k, v := range data {
			base[k] = v
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", base); err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		return buf.String()
	}

	// Группа внутри родителя.
	html := render(map[string]any{"NewIsFolder": true, "NewParentID": "P1"})
	if !strings.Contains(html, `name="is_folder" value="true"`) {
		t.Errorf("скрытое поле is_folder=true не отрисовано:\n%s", html)
	}
	if !strings.Contains(html, `name="parent_id" value="P1"`) {
		t.Errorf("скрытое поле parent_id не отрисовано")
	}

	// Обычный элемент — скрытых полей иерархии нет (Upsert запишет is_folder=false).
	plain := render(nil)
	if strings.Contains(plain, `name="is_folder"`) {
		t.Errorf("для обычного элемента is_folder не должно рендериться:\n%s", plain)
	}
}
