package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Go through the public event route on a persisted object. Only the exact
// native player's transient source/caption are view state, not a blanket
// exemption for save:false attributes, object fields or arbitrary commands.
func TestAudioPlayViewStateHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		for _, tc := range []struct {
			name, source, extra                                       string
			editName, editFlag, saveSource, sharedInput, otherCommand bool
			wantDirty                                                 bool
		}{
			{name: "only-player-view-fields", source: "Source"},
			{name: "real-native-header-input", source: "Source", editName: true, wantDirty: true},
			{name: "real-boolean-input", source: "Source", editFlag: true, wantDirty: true},
			{name: "entity-source-is-not-view-state", source: "StoredSource", wantDirty: true},
			{name: "unrelated-transient-attribute", source: "Source", extra: `Объект.Note = "unsent note";`, wantDirty: true},
			{name: "saved-form-attribute", source: "Source", saveSource: true, wantDirty: true},
			{name: "shared-editable-binding", source: "Source", sharedInput: true, wantDirty: true},
			{name: "ordinary-command-is-not-play", source: "Source", otherCommand: true, wantDirty: true},
			{name: "real-table-part-change", source: "Source", extra: `Ряд = Объект.Lines.Добавить(); Ряд.Text = "unsaved row";`, wantDirty: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				entity := &metadata.Entity{Name: "AudioViewState", Kind: metadata.KindCatalog, Fields: []metadata.Field{
					{Name: "Name", Type: metadata.FieldTypeString}, {Name: "StoredSource", Type: metadata.FieldTypeString}, {Name: "Flag", Type: metadata.FieldTypeBool},
				}, TableParts: []metadata.TablePart{{Name: "Lines", Fields: []metadata.Field{{Name: "Text", Type: metadata.FieldTypeString}}}}}
				if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
					t.Fatal(err)
				}
				form := &metadata.FormModule{Name: "Form", Kind: "object", EntityName: entity.Name, LayoutKind: metadata.FormLayoutManaged,
					Attributes: []*metadata.FormAttribute{{Name: "Source", TypeRef: "string", Save: tc.saveSource}, {Name: "Caption", TypeRef: "string"}, {Name: "Note", TypeRef: "string"}},
					Elements: []*metadata.FormElement{
						{Kind: metadata.FormElementAudio, Name: "Player", DataPath: "Объект." + tc.source, TitleFrom: "Объект.Caption", Handlers: map[metadata.FormEventType]string{metadata.FormEventAudioPlay: "Prepare"}},
						{Kind: metadata.FormElementButton, Name: "Other", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Prepare"}},
					},
				}
				if tc.sharedInput {
					form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementField, Name: "EditableSource", DataPath: "Объект.Source"})
				}
				form.ProgramAST = mustParse(t, "Процедура Prepare()\nОбъект."+tc.source+" = \"11111111-1111-4111-8111-111111111111\";\nОбъект.Caption = \"Call A\";\n"+tc.extra+"\nКонецПроцедуры")
				entity.Forms = []*metadata.FormModule{form}
				reg := runtime.NewRegistry()
				reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
				interp := interpreter.New()
				interp.LookupProc = reg.GetModuleProc
				s := &Server{store: db, reg: reg, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
				s.entitySvc = s.newEntityService(nil)
				id := uuid.New()
				if err := db.Upsert(ctx, entity.Name, id, map[string]any{"Name": "own", "StoredSource": "old", "Flag": false}, entity); err != nil {
					t.Fatal(err)
				}
				body := url.Values{"_kind": {"object"}, "_form": {"Form"}, "_id": {id.String()}, "_version": {"1"}, "_element": {"Player"}, "_event": {string(metadata.FormEventAudioPlay)}, "Name": {"own"}, "StoredSource": {"old"}, "Flag": {"false"}, "Source": {""}, "Caption": {""}, "Note": {""}}
				if tc.editName {
					body.Set("Name", "real unsaved edit")
				}
				if tc.editFlag {
					body.Set("Flag", "true")
				}
				if tc.otherCommand {
					body.Set("_element", "Other")
					body.Set("_event", string(metadata.FormEventOnClick))
				}
				router := chi.NewRouter()
				router.Post("/ui/catalog/{entity}/form-event", s.handleManagedFormEvent)
				r := httptest.NewRequest(http.MethodPost, "/ui/catalog/"+entity.Name+"/form-event", strings.NewReader(body.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				actor := &auth.User{ID: "own", Login: "own", Roles: []*auth.Role{{Name: "Own", Permissions: auth.Permission{Catalogs: map[string][]string{entity.Name: {"read", "write"}}}}}}
				r = r.WithContext(auth.ContextWithUser(r.Context(), actor))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("HTTP%d: %s", w.Code, w.Body.String())
				}
				response := decodeFormEventResponse(t, w.Body.Bytes())
				if !response.OK || response.Error != "" || response.Dirty == nil || *response.Dirty != tc.wantDirty {
					t.Fatalf("dirty=%v want=%v response=%+v", response.Dirty, tc.wantDirty, response)
				}
				stored, err := db.GetByID(ctx, entity.Name, id, entity)
				if err != nil || stored["Name"] != "own" || stored["StoredSource"] != "old" || stored["_version"] != int64(1) {
					t.Fatalf("event wrote persisted object: err=%v", err)
				}
				rows, err := db.GetTablePartRows(ctx, entity.Name, "Lines", id, entity.TableParts[0])
				if err != nil || len(rows) != 0 {
					t.Fatalf("event wrote tablepart: rows=%d err=%v", len(rows), err)
				}
			})
		}
	})
}

func TestAudioPlayViewStateProcessorHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, tc := range []struct {
			name, extra                      string
			saved, shared, sourceParam, want bool
		}{
			{name: "view-only"},
			{name: "real-parameter-change", extra: `Объект.Name = "changed parameter";`, want: true},
			{name: "other-transient-note", extra: `Объект.Note = "unsent note";`, want: true},
			{name: "saved-source", saved: true, want: true},
			{name: "shared-source", shared: true, want: true},
			{name: "source-is-business-parameter", sourceParam: true, want: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementAudio, Name: "Player", DataPath: "Объект.Source", TitleFrom: "Объект.Caption", Handlers: map[metadata.FormEventType]string{metadata.FormEventAudioPlay: "Prepare"}})
				form.Attributes = []*metadata.FormAttribute{{Name: "Source", TypeRef: "string", Save: tc.saved}, {Name: "Caption", TypeRef: "string"}, {Name: "Note", TypeRef: "string"}}
				if tc.shared {
					form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementField, Name: "Shared", DataPath: "Объект.Source"})
				}
				form.ProgramAST = mustParse(t, "Процедура Prepare()\nОбъект.Source = \"11111111-1111-4111-8111-111111111111\"; Объект.Caption = \"Call\";\n"+tc.extra+"\nКонецПроцедуры")
				proc := &processor.Processor{Name: "AudioView", Params: []processor.Param{{Name: "Name", Type: "string"}}, Forms: []*metadata.FormModule{form}}
				if tc.sourceParam {
					proc.Params = append(proc.Params, processor.Param{Name: "Source", Type: "string"})
				}
				reg := runtime.NewRegistry()
				reg.LoadProcessors([]*processor.Processor{proc})
				interp := interpreter.New()
				interp.LookupProc = reg.GetModuleProc
				s := &Server{store: db, reg: reg, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
				s.entitySvc = s.newEntityService(nil)
				body := url.Values{"_element": {"Player"}, "_event": {string(metadata.FormEventAudioPlay)}, "Name": {"own"}}
				router := chi.NewRouter()
				router.Post("/ui/processor/{name}/form-event", s.handleProcessorFormEvent)
				r := httptest.NewRequest(http.MethodPost, "/ui/processor/AudioView/form-event", strings.NewReader(body.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				actor := &auth.User{ID: "own", Login: "own", Roles: []*auth.Role{{Name: "Own", Permissions: auth.Permission{Processors: map[string][]string{proc.Name: {"run"}}}}}}
				r = r.WithContext(auth.ContextWithUser(r.Context(), actor))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("HTTP%d: %s", w.Code, w.Body.String())
				}
				response := decodeFormEventResponse(t, w.Body.Bytes())
				if !response.OK || response.Error != "" || response.Dirty == nil || *response.Dirty != tc.want {
					t.Fatalf("dirty=%v want=%v response=%+v", response.Dirty, tc.want, response)
				}
			})
		}
	})
}

func TestAudioPlayViewStateProjectionBoundaries(t *testing.T) {
	for _, name := range []string{"id", "_id", "_version", "parent_id", "is_folder", "posted", "deletion_mark", "Ссылка", "Reference"} {
		t.Run(name, func(t *testing.T) {
			form := &metadata.FormModule{Attributes: []*metadata.FormAttribute{{Name: name, TypeRef: "string"}}, Elements: []*metadata.FormElement{{Kind: metadata.FormElementAudio, Name: "Player", DataPath: "Объект." + name}}}
			projected, excluded := audioPlaybackComparison(form, nil, "Player", string(metadata.FormEventAudioPlay))
			if projected != form || len(excluded) != 0 {
				t.Fatal("service identity was classified as player view state")
			}
		})
	}
	form := &metadata.FormModule{Attributes: []*metadata.FormAttribute{{Name: "Source", TypeRef: "string"}}, Elements: []*metadata.FormElement{{Kind: metadata.FormElementAudio, Name: "Player", DataPath: "Объект.Source"}}}
	for _, event := range []string{"ПередЗакрытием", "ПередЗаписью", "Нажатие", "ПриИзменении", "Ответ"} {
		projected, excluded := audioPlaybackComparison(form, nil, "Player", event)
		if projected != form || len(excluded) != 0 {
			t.Fatalf("ordinary %s received a player dirty exception", event)
		}
	}
	projected, excluded := audioPlaybackComparison(form, nil, "Other", string(metadata.FormEventAudioPlay))
	if projected != form || len(excluded) != 0 {
		t.Fatal("foreign player name received an exception")
	}
	form.Elements = append(form.Elements, form.Elements[0])
	projected, excluded = audioPlaybackComparison(form, nil, "Player", string(metadata.FormEventAudioPlay))
	if projected != form || len(excluded) != 0 {
		t.Fatal("ambiguous player received an exception")
	}
}
