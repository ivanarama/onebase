package ui

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// The Close/navigation guard uses the same persisted comparison as ordinary
// form events. Clearing/preparing a player's display source on Close is not a
// save operation, but a real entity/table/note mutation must still be refused.
func TestAudioServiceStateCloseMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := auth.ContextWithUser(context.Background(), &auth.User{ID: "own-audio", Login: "own-audio", IsAdmin: true})
		entity := &metadata.Entity{Name: "AudioCloseState", Kind: metadata.KindCatalog,
			Fields:     []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}, {Name: "Flag", Type: metadata.FieldTypeBool}},
			TableParts: []metadata.TablePart{{Name: "Lines", Fields: []metadata.Field{{Name: "Text", Type: metadata.FieldTypeString}}}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		if err := db.Upsert(ctx, entity.Name, id, map[string]any{"Name": "own", "Flag": false}, entity); err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
		s := &Server{store: db, reg: reg}
		for _, tc := range []struct {
			name                                                               string
			saved, editable, nameEdit, flagEdit, noteEdit, tableEdit, noPlayer bool
			want                                                               bool
		}{
			{name: "close-clears-view-state"},
			{name: "source-is-saved", saved: true, want: true},
			{name: "source-has-editable-binding", editable: true},
			{name: "real-entity-before-or-after-play", nameEdit: true, want: true},
			{name: "real-bool-before-or-after-play", flagEdit: true, want: true},
			{name: "real-form-note-before-or-after-play", noteEdit: true, want: true},
			{name: "real-table-part-before-or-after-play", tableEdit: true, want: true},
			{name: "non-player-form-stays-guarded", noPlayer: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				form := &metadata.FormModule{Name: "OwnForm", Attributes: []*metadata.FormAttribute{
					{Name: "Source", TypeRef: "string", Save: tc.saved}, {Name: "Caption", TypeRef: "string"}, {Name: "Note", TypeRef: "string", Save: true},
				}}
				if !tc.noPlayer {
					form.Elements = []*metadata.FormElement{{Kind: metadata.FormElementAudio, Name: "Player", DataPath: "Объект.Source", TitleFrom: "Объект.Caption"},
						{Kind: metadata.FormElementField, Name: "SourceDisplay", DataPath: "Объект.Source", ReadOnly: !tc.editable}}
				}
				fields := map[string]any{"Name": "own", "Flag": false, "Source": "cached-own-recording", "Caption": "Own call", "Note": ""}
				before := snapshotFieldValues(fields)
				obj := &runtime.Object{ID: id, Type: entity.Name, Kind: entity.Kind, Fields: fields, TablePartRows: map[string][]map[string]any{"Lines": nil}}
				// The lifecycle handler clears display state. No save is involved.
				obj.Fields["Source"] = ""
				obj.Fields["Caption"] = "No recording"
				if tc.nameEdit {
					obj.Fields["Name"] = "unsaved native edit"
				}
				if tc.flagEdit {
					obj.Fields["Flag"] = true
				}
				if tc.noteEdit {
					obj.Fields["Note"] = "unsaved form edit"
				}
				if tc.tableEdit {
					obj.TablePartRows["Lines"] = []map[string]any{{"Text": "unsaved row"}}
				}
				dirty := s.managedCloseStateDirty(ctx, entity, form, obj, before, map[string][]map[string]any{"Lines": nil})
				if dirty != tc.want {
					t.Fatalf("dirty=%t want=%t", dirty, tc.want)
				}
				stored, err := db.GetByID(ctx, entity.Name, id, entity)
				if err != nil || stored["Name"] != "own" || stored["_version"] != int64(1) {
					t.Fatal("comparison wrote the object")
				}
			})
		}
	})
}
