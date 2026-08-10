package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Половина фикса #618 не работала: признак группы ставился, а родитель терялся —
// hierarchyCreateHints читал ?parent_id=, которого не шлёт никто, тогда как
// кнопки списка и автоформа называют этот параметр ?parent=. Создание внутри
// группы молча клало запись в корень (#615).
//
// Тест намеренно связывает ПРОИЗВОДИТЕЛЯ ссылки и её ПОТРЕБИТЕЛЯ: берёт адрес
// прямо из отрисованной кнопки списка и идёт по нему. Прежний тест звал
// hierarchyCreateHints напрямую с рукописным query и потому был зелёным на
// имени параметра, которого в проде нет.

var hrefRe = regexp.MustCompile(`href="([^"]*/new\?[^"]*)"`)

func TestManagedForm_РодительИзКнопкиСпискаДоезжает(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Папки", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		Forms: []*metadata.FormModule{{
			Name: "ФормаОбъекта", Kind: "object", EntityName: "Папки",
			LayoutKind: metadata.FormLayoutManaged,
			Elements:   []*metadata.FormElement{{Kind: "ПолеВвода", Name: "Наименование", DataPath: "Объект.Наименование"}},
		}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})

	parentID := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, parentID, map[string]any{
		"Наименование": "Группа", "is_folder": true,
	}, ent); err != nil {
		t.Fatalf("создание группы: %v", err)
	}

	r := chi.NewRouter()
	s.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	// 1. Список, открытый ВНУТРИ группы.
	listURL := ts.URL + "/ui/catalog/" + url.PathEscape(ent.Name) + "?parent=" + parentID.String()
	body := getBody(t, listURL)

	// 2. Берём адрес кнопки создания прямо из разметки — как кликнул бы человек.
	var createHref string
	for _, m := range hrefRe.FindAllStringSubmatch(body, -1) {
		if strings.Contains(m[1], parentID.String()) {
			createHref = m[1]
			break
		}
	}
	if createHref == "" {
		t.Fatalf("в списке нет кнопки создания с родителем %s", parentID)
	}

	// 3. Идём по нему и проверяем, что форма унесёт родителя в запись.
	formBody := getBody(t, ts.URL+strings.ReplaceAll(createHref, "&amp;", "&"))
	if !strings.Contains(formBody, parentID.String()) {
		t.Errorf("управляемая форма не получила родителя — запись улетит в корень.\nссылка: %s", createHref)
	}
}

func getBody(t *testing.T, u string) string {
	t.Helper()
	resp, err := http.Get(u) //nolint:gosec,noctx // адрес собран тестовым сервером
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close() //nolint:errcheck // тело читается ниже
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение тела: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s → %d: %s", u, resp.StatusCode, string(b))
	}
	return string(b)
}
