package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Подчинение справочника (owner) и choice_filter ограничивают ОДИН и тот же
// подбор. Данные подобраны так, что поодиночке ни одно ограничение не даёт
// верного ответа: у обоих направлений одна и та же группа неисправностей,
// поэтому отбор по группе не отделяет чужое направление, а отбор по владельцу
// не отделяет чужую группу. Верен только их одновременный результат.
type ownerAndFilterFixture struct {
	server     *Server
	target     *metadata.Entity
	owner      *metadata.Entity
	directionA uuid.UUID
	directionB uuid.UUID
	ownFault   uuid.UUID // владелец A, группа G1 — единственный верный ответ
	foreignOwn uuid.UUID // владелец B, группа G1 — отсекается владельцем
	otherGroup uuid.UUID // владелец A, группа G2 — отсекается choice_filter
}

func newOwnerAndFilterFixture(t *testing.T) ownerAndFilterFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "owner-choice.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	group := &metadata.Entity{
		Name: "ГруппаНеисправностей", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	direction := &metadata.Entity{
		Name: "Направление", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ГруппаНеисправностей", Type: metadata.FieldType("reference:" + group.Name), RefEntity: group.Name},
		},
	}
	target := &metadata.Entity{
		Name: "Неисправность", Kind: metadata.KindCatalog, Owner: direction.Name,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Группа", Type: metadata.FieldType("reference:" + group.Name), RefEntity: group.Name},
			{
				Name: metadata.StandardOwnerField, ID: metadata.StandardOwnerFieldID,
				Type: metadata.FieldType("reference:" + direction.Name), RefEntity: direction.Name,
			},
		},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", EntityName: "Заявка", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{ID: "direction", Name: "ПолеНаправление", Kind: metadata.FormElementField, DataPath: "Объект.Направление"},
			{
				ID: "fault", Name: "ПолеНеисправность", Kind: metadata.FormElementField,
				DataPath: "Объект.Неисправность", Choice: true,
				ChoiceFilter: []metadata.FormChoiceCondition{{
					Field: "Группа", Op: metadata.FormChoiceOpEqual,
					From: "Объект.Направление.ГруппаНеисправностей",
				}},
			},
		},
	}
	owner := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Направление", Type: metadata.FieldType("reference:" + direction.Name), RefEntity: direction.Name},
			{Name: "Неисправность", Type: metadata.FieldType("reference:" + target.Name), RefEntity: target.Name},
		},
		Forms: []*metadata.FormModule{form},
	}
	entities := []*metadata.Entity{group, direction, target, owner}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}

	groupOne := uuid.MustParse("60000000-0000-0000-0000-000000000001")
	groupTwo := uuid.MustParse("60000000-0000-0000-0000-000000000002")
	fixture := ownerAndFilterFixture{
		server: nil, target: target, owner: owner,
		directionA: uuid.MustParse("70000000-0000-0000-0000-000000000001"),
		directionB: uuid.MustParse("70000000-0000-0000-0000-000000000002"),
		ownFault:   uuid.MustParse("80000000-0000-0000-0000-000000000001"),
		foreignOwn: uuid.MustParse("80000000-0000-0000-0000-000000000002"),
		otherGroup: uuid.MustParse("80000000-0000-0000-0000-000000000003"),
	}
	for id, name := range map[uuid.UUID]string{groupOne: "Группа 1", groupTwo: "Группа 2"} {
		if err := db.Upsert(ctx, group.Name, id, map[string]any{"Наименование": name}, group); err != nil {
			t.Fatalf("seed group: %v", err)
		}
	}
	// У ОБОИХ направлений одна группа: разделяет их только владелец.
	for id, name := range map[uuid.UUID]string{fixture.directionA: "Направление A", fixture.directionB: "Направление B"} {
		if err := db.Upsert(ctx, direction.Name, id, map[string]any{
			"Наименование": name, "ГруппаНеисправностей": groupOne.String(),
		}, direction); err != nil {
			t.Fatalf("seed direction: %v", err)
		}
	}
	for _, row := range []struct {
		id    uuid.UUID
		name  string
		owner uuid.UUID
		group uuid.UUID
	}{
		{fixture.ownFault, "своя неисправность", fixture.directionA, groupOne},
		{fixture.foreignOwn, "чужое направление", fixture.directionB, groupOne},
		{fixture.otherGroup, "чужая группа", fixture.directionA, groupTwo},
	} {
		if err := db.Upsert(ctx, target.Name, row.id, map[string]any{
			"Наименование": row.name, "Группа": row.group.String(),
			metadata.StandardOwnerField: row.owner.String(),
		}, target); err != nil {
			t.Fatalf("seed fault: %v", err)
		}
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	fixture.server = &Server{reg: reg, store: db}
	fixture.server.entitySvc = fixture.server.newEntityService(nil)
	return fixture
}

func (f ownerAndFilterFixture) user() *auth.User {
	return &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs:  map[string][]string{"Направление": {"read"}, "Неисправность": {"read"}, "ГруппаНеисправностей": {"read"}},
		Documents: map[string][]string{"Заявка": {"read", "write"}},
	}}}}
}

// options повторяет запрос браузера: контекст формы для choice_filter и flt для
// подчинения. Значения источников и владельца берутся с той же формы.
func (f ownerAndFilterFixture) options(t *testing.T, directionSource, ownerFilter, selected string) choiceHTTPResponse {
	t.Helper()
	sources, err := json.Marshal(map[string]string{"Объект.Направление.ГруппаНеисправностей": directionSource})
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{
		"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"},
		"element": {"fault"}, "sources": {string(sources)}, "limit": {"100"},
	}
	if ownerFilter != "" {
		filter, mErr := json.Marshal(map[string]string{metadata.StandardOwnerField: ownerFilter})
		if mErr != nil {
			t.Fatal(mErr)
		}
		query.Set("flt", string(filter))
	}
	if selected != "" {
		query.Set("selected_id", selected)
	}
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?"+query.Encode(), nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), f.user()))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	return decodeChoiceHTTP(t, recorder)
}

func ownerChoiceLabels(rows []map[string]any) []string {
	labels := make([]string, 0, len(rows))
	for _, row := range rows {
		labels = append(labels, fmt.Sprint(row["_label"]))
	}
	return labels
}

func TestRefOptionsCombinesOwnerAndChoiceFilter(t *testing.T) {
	f := newOwnerAndFilterFixture(t)

	t.Run("оба ограничения действуют одновременно", func(t *testing.T) {
		response := f.options(t, f.directionA.String(), f.directionA.String(), "")
		labels := ownerChoiceLabels(response.Items)
		if len(labels) != 1 || labels[0] != "своя неисправность" {
			t.Fatalf("сочетание отборов дало не тот состав: %v", labels)
		}
		if response.Total != 1 {
			t.Fatalf("итог не совпал с выдачей: total=%d %v", response.Total, labels)
		}
	})

	t.Run("без владельца остаётся чужое направление", func(t *testing.T) {
		labels := ownerChoiceLabels(f.options(t, f.directionA.String(), "", "").Items)
		if len(labels) != 2 {
			t.Fatalf("ожидались обе неисправности группы 1: %v", labels)
		}
	})

	t.Run("без choice_filter остаётся чужая группа", func(t *testing.T) {
		// Без контекста формы: так ходит поле, которое choice_filter не объявляло.
		filter, err := json.Marshal(map[string]string{metadata.StandardOwnerField: f.directionA.String()})
		if err != nil {
			t.Fatal(err)
		}
		query := url.Values{"limit": {"100"}, "flt": {string(filter)}}
		router := chi.NewRouter()
		f.server.Mount(router)
		request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?"+query.Encode(), nil)
		request = request.WithContext(auth.ContextWithUser(request.Context(), f.user()))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		labels := ownerChoiceLabels(decodeChoiceHTTP(t, recorder).Items)
		if len(labels) != 2 {
			t.Fatalf("ожидались обе неисправности направления A: %v", labels)
		}
	})

	t.Run("выбранное значение чужого владельца недопустимо", func(t *testing.T) {
		response := f.options(t, f.directionA.String(), f.directionA.String(), f.foreignOwn.String())
		if response.SelectedAllowed == nil || *response.SelectedAllowed {
			t.Fatalf("чужое направление объявлено допустимым: %s", mustJSONOwner(t, response))
		}
	})

	t.Run("выбранное значение чужой группы недопустимо", func(t *testing.T) {
		response := f.options(t, f.directionA.String(), f.directionA.String(), f.otherGroup.String())
		if response.SelectedAllowed == nil || *response.SelectedAllowed {
			t.Fatalf("чужая группа объявлена допустимой: %s", mustJSONOwner(t, response))
		}
	})

	t.Run("пустой глубокий источник не раскрывает справочник", func(t *testing.T) {
		labels := ownerChoiceLabels(f.options(t, "", f.directionA.String(), "").Items)
		if len(labels) != 0 {
			t.Fatalf("без направления показаны неисправности: %v", labels)
		}
	})
}

func mustJSONOwner(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
