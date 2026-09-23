package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Колонки SlickGrid уезжают в data-sg-cols. Чтобы подбор из ячейки ТЧ мог
// предложить «+ Создать», признак allow_inline_create должен доехать до
// клиента: без него редактор открывает форму выбора без создания (issue #765).
func TestManagedGridColumnsCarryInlineCreate(t *testing.T) {
	yes := true
	ent := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
			{Name: "КлиентБезСоздания", Type: "reference:Клиент", RefEntity: "Клиент"},
			{Name: "КлиентСоСозданием", Type: "reference:Клиент", RefEntity: "Клиент", AllowInlineCreate: &yes},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		}}},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки",
		}},
	}

	var buf bytes.Buffer
	err := tmpl.ExecuteTemplate(&buf, "page-managed-form", map[string]any{
		"Entity": ent, "Form": form, "IsNew": true, "CanWrite": true,
		"RefWriteAccess": map[string]bool{"Клиент": true},
		"Values":         map[string]string{}, "RefOptions": map[string]any{},
		"EnumOptions": map[string]any{}, "TablePartRows": map[string][]map[string]any{},
		"TPRefOptions": map[string]any{}, "TPEnumLabels": map[string]any{},
		"Lang": "ru",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	cols := parseManagedTPColumns(t, buf.String())
	byID := make(map[string]managedTPColumnJSON, len(cols))
	for _, col := range cols {
		byID[col.ID] = col
	}
	if !byID["КлиентСоСозданием"].AllowCreate {
		t.Errorf("колонка с allow_inline_create не отдала allowCreate клиенту: %+v", cols)
	}
	if byID["КлиентБезСоздания"].AllowCreate {
		t.Errorf("создание включилось у колонки без allow_inline_create (в ТЧ дефолт — выключено): %+v", cols)
	}
	if byID["Количество"].AllowCreate {
		t.Errorf("allowCreate протёк в нессылочную колонку: %+v", cols)
	}
}

func TestEntityFormInlineCreateRequiresReferencedEntityWrite(t *testing.T) {
	for _, managed := range []bool{false, true} {
		name := "auto"
		if managed {
			name = "managed"
		}
		t.Run(name, func(t *testing.T) {
			yes := true
			city := &metadata.Entity{Name: "Город", Kind: metadata.KindCatalog}
			doc := &metadata.Entity{
				Name: "Обращение", Kind: metadata.KindDocument,
				Fields: []metadata.Field{{
					Name: "Город", Type: "reference:Город", RefEntity: "Город",
				}},
				TableParts: []metadata.TablePart{{Name: "Контакты", Fields: []metadata.Field{{
					Name: "Город", Type: "reference:Город", RefEntity: "Город", AllowInlineCreate: &yes,
				}}}},
			}
			if managed {
				doc.Forms = []*metadata.FormModule{{
					Name: "ФормаОбъекта", Kind: "object", EntityName: doc.Name,
					LayoutKind: metadata.FormLayoutManaged,
					Elements: []*metadata.FormElement{
						{Kind: metadata.FormElementField, Name: "Город", DataPath: "Объект.Город"},
						{Kind: metadata.FormElementTablePart, Name: "Контакты", DataPath: "Объект.Контакты"},
					},
				}}
			}

			s, _ := newSubmitTestServer(t, []*metadata.Entity{city, doc})
			render := func(catalogOps []string) string {
				t.Helper()
				user := &auth.User{Login: "operator", Roles: []*auth.Role{{Permissions: auth.Permission{
					Catalogs:  map[string][]string{city.Name: catalogOps},
					Documents: map[string][]string{doc.Name: {"write"}},
				}}}}
				req := reqWithChi(http.MethodGet, "/ui/document/Обращение/new", nil, map[string]string{
					"kind": "document", "entity": doc.Name,
				})
				req = req.WithContext(auth.ContextWithUser(req.Context(), user))
				rec := httptest.NewRecorder()
				s.form(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("GET form: status=%d body=%s", rec.Code, rec.Body.String())
				}
				return rec.Body.String()
			}

			denied := render([]string{"read"})
			if !strings.Contains(denied, `data-ref-entity="Город"`) {
				t.Fatal("публичный HTTP-рендер потерял ссылочное поле")
			}
			if strings.Contains(denied, `data-ref-allow-create="1"`) {
				t.Fatal("read-only право целевого справочника разрешило inline-создание")
			}
			if managed {
				cols := parseManagedTPColumns(t, denied)
				if len(cols) != 1 || cols[0].AllowCreate {
					t.Fatalf("SlickGrid обошёл отсутствие write на целевом справочнике: %+v", cols)
				}
			}

			allowed := render([]string{"read", "write"})
			if !strings.Contains(allowed, `data-ref-allow-create="1"`) {
				t.Fatal("право write целевого справочника не разрешило inline-создание")
			}
			if managed {
				cols := parseManagedTPColumns(t, allowed)
				if len(cols) != 1 || !cols[0].AllowCreate {
					t.Fatalf("SlickGrid не получил разрешённое inline-создание: %+v", cols)
				}
			}
		})
	}
}
