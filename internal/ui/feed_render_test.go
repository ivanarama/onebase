package ui

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// TestResolveListMode: приоритет режима списка — явный ?lm > кука per-сущность >
// дефолт сущности; явный ?lm запоминается в куку.
func TestResolveListMode(t *testing.T) {
	s := &Server{}
	entPages := &metadata.Entity{Name: "Док", Kind: metadata.KindDocument}
	entFeed := &metadata.Entity{Name: "Тов", Kind: metadata.KindCatalog, ListMode: "feed"}

	if s.resolveListMode(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), entPages) {
		t.Error("ожидался pages по умолчанию")
	}
	if !s.resolveListMode(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), entFeed) {
		t.Error("ожидался feed по дефолту сущности")
	}

	// явный ?lm=feed включает ленту и ставит куку
	w := httptest.NewRecorder()
	if !s.resolveListMode(w, httptest.NewRequest("GET", "/?lm=feed", nil), entPages) {
		t.Error("?lm=feed должен включить ленту")
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("ожидалась установка куки режима")
	}

	// кука перебивает дефолт сущности
	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	if !s.resolveListMode(httptest.NewRecorder(), r, entPages) {
		t.Error("кука feed должна включить ленту для той же сущности")
	}
}

func feedEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Товар", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
}

func renderPageList(t *testing.T, data map[string]any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-list", data); err != nil {
		t.Fatalf("ExecuteTemplate page-list: %v", err)
	}
	return buf.String()
}

// TestPageList_FeedMode: режим «лента» рендерит сентинел догрузки (#feed-more)
// с селекторами контейнера/элемента и кнопкой «Показать ещё», а номерную
// пагинацию («Вперёд →») НЕ показывает. Тумблер предлагает вернуться к страницам.
func TestPageList_FeedMode(t *testing.T) {
	base := map[string]any{
		"Entity":           feedEntity(),
		"Rows":             []map[string]any{{"id": "1", "Наименование": "A"}},
		"Params":           storage.ListParams{},
		"RefFilterOptions": map[string]any{},
		"Lang":             "ru",
		"Feed":             true,
		"Total":            250, "Page": 1, "TotalPages": 3, "HasNext": true, "NextPage": 2,
	}
	html := renderPageList(t, base)
	for _, want := range []string{
		`id="feed-more"`,
		`data-container="#list-body"`,
		`data-item="tr"`,
		"Показать ещё",
		"page=2", "lm=feed", // ссылка догрузки (& экранируется в &amp;, проверяем по частям)
		"lm=pages", // тумблер предлагает вернуться к страницам
	} {
		if !strings.Contains(html, want) {
			t.Errorf("feed: нет %q", want)
		}
	}
	if strings.Contains(html, "Вперёд") {
		t.Error("feed: номерная пагинация не должна показываться")
	}

	// tiles + feed → контейнер .tile-grid / .tile-card
	base["TilesView"] = true
	htmlTiles := renderPageList(t, base)
	for _, want := range []string{`data-container=".tile-grid"`, `data-item=".tile-card"`} {
		if !strings.Contains(htmlTiles, want) {
			t.Errorf("feed+tiles: нет %q", want)
		}
	}
}

// TestPageList_PagesModeDefault: без Feed показывается номерная пагинация, а
// тумблер предлагает переключиться на ленту (lm=feed); сентинела ленты нет.
func TestPageList_PagesModeDefault(t *testing.T) {
	html := renderPageList(t, map[string]any{
		"Entity":           feedEntity(),
		"Rows":             []map[string]any{{"id": "1", "Наименование": "A"}},
		"Params":           storage.ListParams{},
		"RefFilterOptions": map[string]any{},
		"Lang":             "ru",
		"Feed":             false,
		"Total":            250, "Page": 1, "TotalPages": 3, "HasNext": true, "NextPage": 2,
	})
	if strings.Contains(html, `id="feed-more"`) {
		t.Error("pages: сентинел ленты не должен рендериться")
	}
	for _, want := range []string{"Вперёд", "lm=feed"} {
		if !strings.Contains(html, want) {
			t.Errorf("pages: нет %q", want)
		}
	}
}
