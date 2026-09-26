package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Сортировка списка должна переживать уход в другой список и возврат: без
// этого порядок живёт только в адресе страницы, и выбор пользователя теряется
// при первом же переходе.
func TestResolveListSortRemembersChoice(t *testing.T) {
	ent := &metadata.Entity{
		Kind:   metadata.KindDocument,
		Name:   "Заявка",
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	s := &Server{}

	// Явный выбор — запоминаем в куку.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ui/documents/Заявка?sort=Номер&dir=desc", nil)
	params := storage.ListParams{Sort: "Номер", Dir: "desc"}
	s.resolveListSort(w, r, ent, &params)
	cookies := w.Result().Cookies()
	var saved *http.Cookie
	for _, c := range cookies {
		if c.Name == listSortCookie {
			saved = c
		}
	}
	if saved == nil {
		t.Fatalf("сортировка не сохранена в куку: %v", cookies)
	}

	// Возврат по чистому адресу — порядок восстанавливается.
	r2 := httptest.NewRequest(http.MethodGet, "/ui/documents/Заявка", nil)
	r2.AddCookie(saved)
	restored := storage.ListParams{}
	s.resolveListSort(httptest.NewRecorder(), r2, ent, &restored)
	if restored.Sort != "Номер" || restored.Dir != "desc" {
		t.Errorf("сортировка не восстановлена: sort=%q dir=%q", restored.Sort, restored.Dir)
	}
}

// Поле могло исчезнуть из метаданных: подставлять его в ORDER BY нельзя.
func TestResolveListSortIgnoresUnknownField(t *testing.T) {
	ent := &metadata.Entity{
		Kind:   metadata.KindDocument,
		Name:   "Заявка",
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
	}
	s := &Server{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ui/documents/Заявка?sort=УдалённоеПоле&dir=asc", nil)
	params := storage.ListParams{Sort: "УдалённоеПоле", Dir: "asc"}
	s.resolveListSort(w, r, ent, &params)

	var saved *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == listSortCookie {
			saved = c
		}
	}
	if saved == nil {
		t.Fatalf("кука не выставлена")
	}
	r2 := httptest.NewRequest(http.MethodGet, "/ui/documents/Заявка", nil)
	r2.AddCookie(saved)
	restored := storage.ListParams{}
	s.resolveListSort(httptest.NewRecorder(), r2, ent, &restored)
	if restored.Sort != "" {
		t.Errorf("сортировка по несуществующему полю восстановлена: %q", restored.Sort)
	}
}
