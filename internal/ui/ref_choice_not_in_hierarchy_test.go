package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// not_in_hierarchy (#1821) через публичный маршрут подбора: точное дополнение
// in_hierarchy, пустой реквизит проходит. Исключаемая группа — из ref или из
// поля формы (в том числе за одной ссылкой) — обязана быть видна
// пользователю: несуществующая дала бы весь справочник, закрытая RLS —
// показала бы, что лежит у неё внутри. В обоих случаях подбор пуст.
func newNotInHierarchyFixture(t *testing.T, db *storage.DB) refChoiceFixture {
	t.Helper()
	notFolder := metadata.FormChoiceCondition{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: boolPtr(false)}
	notIn := func(field string, condition metadata.FormChoiceCondition) metadata.FormChoiceCondition {
		condition.Field, condition.Op = field, metadata.FormChoiceOpNotInHierarchy
		return condition
	}
	missing := choiceHTTPUUID(0x7f, 1)
	return newRefChoiceFixture(t, db,
		// Поле-источник формы: что исключать, выбирает пользователь.
		&metadata.FormElement{ID: "source", Name: "ПолеНеИз", Kind: metadata.FormElementField, DataPath: "Объект.НеИз"},
		refChoiceElement("not-archive", "КромеАрхива", notIn("parent_id", metadata.FormChoiceCondition{Ref: choiceHTTPUUID(0x70, 3).String()}), notFolder),
		refChoiceElement("not-analog", "АналогНеРабочий", notIn("Аналог", metadata.FormChoiceCondition{Ref: choiceHTTPUUID(0x70, 1).String()}), notFolder),
		refChoiceElement("not-from", "КромеВыбранной", notIn("parent_id", metadata.FormChoiceCondition{From: "Объект.НеИз"}), notFolder),
		refChoiceElement("not-deep", "КромеАналога", notIn("parent_id", metadata.FormChoiceCondition{From: "Объект.НеИз.Аналог"}), notFolder),
		refChoiceElement("not-missing", "КромеПропавшей", notIn("parent_id", metadata.FormChoiceCondition{Ref: missing.String()}), notFolder),
		refChoiceElement("not-hidden", "КромеСекретной", notIn("parent_id", metadata.FormChoiceCondition{Ref: choiceHTTPUUID(0x70, 4).String()}), notFolder),
	)
}

func (f refChoiceFixture) fetchSources(t *testing.T, element string, sources map[string]string, selected uuid.UUID) choiceHTTPResponse {
	t.Helper()
	query := url.Values{"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"}, "element": {element}, "limit": {"100"}}
	if len(sources) > 0 {
		raw, err := json.Marshal(sources)
		if err != nil {
			t.Fatal(err)
		}
		query.Set("sources", string(raw))
	}
	if selected != uuid.Nil {
		query.Set("selected_id", selected.String())
	}
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?"+query.Encode(), nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), f.anna))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("element %s: status=%d body=%s", element, recorder.Code, recorder.Body.String())
	}
	return decodeChoiceHTTP(t, recorder)
}

// Видимые anna элементы: замена, консультация, старая, тайна, корневая
// («закрытая» скрыта RLS цели).
func TestRefOptionsNotInHierarchy(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newNotInHierarchyFixture(t, db)
		for _, tc := range []struct {
			name    string
			element string
			sources map[string]string
			want    string
		}{
			{"parent_id: всё, кроме архива", "not-archive", nil, "замена,консультация,корневая,тайна"},
			{"ссылочный реквизит: пустой проходит, ветка «Рабочие» исключена", "not-analog", nil, "замена,консультация,корневая,тайна"},
			{"группа из поля формы", "not-from", map[string]string{"Объект.НеИз": f.working.String()}, "корневая,старая,тайна"},
			{"группа за одной ссылкой", "not-deep", map[string]string{"Объект.НеИз.Аналог": f.old.String()}, "замена,консультация,корневая,старая,тайна"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := f.fetchSources(t, tc.element, tc.sources, uuid.Nil)
				if labels := refChoiceLabels(got); labels != tc.want || got.Total != len(got.Items) {
					t.Fatalf("total=%d items=%s, ожидалось %s", got.Total, labels, tc.want)
				}
			})
		}
	})
}

// Fail-closed: исключаемой группы нет, она закрыта RLS или не задана —
// подбор пуст и total = 0. Без этого «всё, кроме невидимой Секретной»
// показало бы весь справочник без «тайны» и раскрыло бы, что она там.
func TestRefOptionsNotInHierarchyUnavailableIsClosed(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newNotInHierarchyFixture(t, db)
		for _, tc := range []struct {
			name    string
			element string
			sources map[string]string
		}{
			{"ref: записи нет в базе", "not-missing", nil},
			{"ref: запись закрыта RLS", "not-hidden", nil},
			{"from: записи нет в базе", "not-from", map[string]string{"Объект.НеИз": choiceHTTPUUID(0x7f, 2).String()}},
			{"from: запись закрыта RLS", "not-from", map[string]string{"Объект.НеИз": f.secret.String()}},
			{"from: источник пуст", "not-from", nil},
			{"за ссылкой: группа закрыта RLS", "not-deep", map[string]string{"Объект.НеИз.Аналог": f.consult.String()}},
			{"за ссылкой: у посредника пусто", "not-deep", map[string]string{"Объект.НеИз.Аналог": f.root.String()}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := f.fetchSources(t, tc.element, tc.sources, uuid.Nil)
				if got.Total != 0 || len(got.Items) != 0 {
					t.Fatalf("недоступная группа открыла выдачу: total=%d items=%s", got.Total, refChoiceLabels(got))
				}
			})
		}
	})
}

// selected_allowed — тот же отбор и та же закрытость.
func TestRefOptionsNotInHierarchySelectedAllowed(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newNotInHierarchyFixture(t, db)
		working := map[string]string{"Объект.НеИз": f.working.String()}
		for _, tc := range []struct {
			name     string
			element  string
			sources  map[string]string
			selected uuid.UUID
			allowed  bool
		}{
			{"элемент вне архива", "not-archive", nil, f.replace, true},
			{"элемент архива", "not-archive", nil, f.old, false},
			{"корневая запись", "not-archive", nil, f.root, true},
			{"папка (is_folder=false)", "not-archive", nil, f.archive, false},
			{"закрытый RLS цели", "not-archive", nil, f.closed, false},
			{"ref нет в базе", "not-missing", nil, f.root, false},
			{"ref закрыт RLS", "not-hidden", nil, f.root, false},
			{"from: элемент выбранной ветки", "not-from", working, f.replace, false},
			{"from: элемент чужой ветки", "not-from", working, f.old, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := f.fetchSources(t, tc.element, tc.sources, tc.selected)
				if got.SelectedAllowed == nil || *got.SelectedAllowed != tc.allowed {
					t.Fatalf("selected_allowed=%v, ожидалось %v", got.SelectedAllowed, tc.allowed)
				}
			})
		}
	})
}
