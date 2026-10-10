package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Группа, уже выбранная через choice_folders, допустима как контекст соседнего
// подбора. Проверяем оба запроса через смонтированные публичные маршруты.
func TestChoicePreviewPageAcceptsGroupContextWithoutExpandingPicker(t *testing.T) {
	for _, tc := range []struct {
		name       string
		group      bool
		folders    bool
		restricted bool
		noRead     bool
		foreign    bool
		missing    bool
		invalid    bool
		inactive   bool
		wantStatus int
	}{
		{name: "group context", group: true, wantStatus: http.StatusOK},
		{name: "item context", wantStatus: http.StatusOK},
		{name: "target includes groups", group: true, folders: true, wantStatus: http.StatusOK},
		{name: "RLS denies source group", group: true, restricted: true, wantStatus: http.StatusForbidden},
		{name: "RLS allows source item", restricted: true, wantStatus: http.StatusOK},
		{name: "inactive source group", group: true, inactive: true, wantStatus: http.StatusForbidden},
		{name: "source read denied", group: true, noRead: true, wantStatus: http.StatusForbidden},
		{name: "foreign catalog UUID", foreign: true, wantStatus: http.StatusForbidden},
		{name: "missing source UUID", missing: true, wantStatus: http.StatusForbidden},
		{name: "invalid source UUID", invalid: true, wantStatus: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newChoiceFoldersFixture(t)
			// Источник и цель — разные справочники: права источника проверяются
			// независимо от read целевого подбора.
			target := &metadata.Entity{
				Name: "Улицы", Kind: metadata.KindCatalog, Hierarchical: true,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			}
			foreign := &metadata.Entity{
				Name: "ЧужойСправочник", Kind: metadata.KindCatalog,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			}
			ctx := context.Background()
			if err := f.server.store.Migrate(ctx, []*metadata.Entity{target, foreign}); err != nil {
				t.Fatal(err)
			}
			itemID, groupID, foreignID := uuid.New(), uuid.New(), uuid.New()
			for _, row := range []struct {
				entity *metadata.Entity
				id     uuid.UUID
				name   string
				group  bool
			}{
				{target, itemID, "Улица", false},
				{target, groupID, "Группа улиц", true},
				{foreign, foreignID, "Чужая запись", false},
			} {
				fields := map[string]any{"Наименование": row.name}
				if row.entity.Hierarchical {
					fields["is_folder"] = row.group
				}
				if err := f.server.store.Upsert(ctx, row.entity.Name, row.id, fields, row.entity); err != nil {
					t.Fatal(err)
				}
			}
			f.owner.Fields[1].RefEntity = target.Name
			f.owner.Fields[1].Type = metadata.FieldType("reference:" + target.Name)
			picker := f.owner.Forms[0].Elements[1]
			picker.ChoiceContext = map[string]string{"Город": "Объект.НаселённыйПункт"}
			picker.ChoiceFolders = tc.folders
			f.server.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{f.target, f.owner, target, foreign}})
			permissions := &f.user.Roles[0].Permissions
			permissions.Catalogs[target.Name] = []string{"read"}

			// Пользователь действительно может выбрать источник-группу.
			selected := f.options(t, "city", url.Values{"selected_id": {f.city.String()}})
			if selected.SelectedAllowed == nil || !*selected.SelectedAllowed {
				t.Fatalf("source group cannot be selected: %s", mustJSON(t, selected))
			}
			if tc.restricted {
				permissions.RowAccess = auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
					f.target.Name: {"read": {Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "улица Ленина"}}},
				}}
			}
			if tc.inactive {
				f.target.Fields = append(f.target.Fields, metadata.Field{Name: "Активный", Type: metadata.FieldTypeBool})
				f.target.Activity = &metadata.ActivityConfig{Field: "Активный", HideFromChoice: true}
				if err := f.server.store.Migrate(ctx, []*metadata.Entity{f.target}); err != nil {
					t.Fatal(err)
				}
				if err := f.server.store.Upsert(ctx, f.target.Name, f.city, map[string]any{"Активный": false}, f.target); err != nil {
					t.Fatal(err)
				}
			}
			if tc.noRead {
				delete(permissions.Catalogs, f.target.Name)
			}
			contextID := f.street.String()
			switch {
			case tc.group:
				contextID = f.city.String()
			case tc.foreign:
				contextID = foreignID.String()
			case tc.missing:
				contextID = uuid.NewString()
			case tc.invalid:
				contextID = "not-a-uuid"
			}
			body := map[string]any{
				"source":  map[string]string{"entity": f.owner.Name, "element": picker.Name},
				"context": map[string]string{"Город": contextID},
			}
			raw := mustJSON(t, body)
			request := httptest.NewRequest(http.MethodPost, "/ui/_ref-options/"+url.PathEscape(target.Name)+"/page", strings.NewReader(raw))
			request = request.WithContext(auth.ContextWithUser(request.Context(), f.user))
			router := chi.NewRouter()
			f.server.Mount(router)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			var result struct {
				Items []map[string]any `json:"items"`
				Total int              `json:"total"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			wantTotal := 1
			if tc.folders {
				wantTotal = 2
			}
			if len(result.Items) != wantTotal || result.Total != wantTotal {
				t.Fatalf("target picker: items=%v total=%d want=%d", result.Items, result.Total, wantTotal)
			}
			for _, item := range result.Items {
				if !tc.folders && item["id"] != itemID.String() {
					t.Fatalf("source group expanded target picker: %v", result.Items)
				}
			}
		})
	}
}
