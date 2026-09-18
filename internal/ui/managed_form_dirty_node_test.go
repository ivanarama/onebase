package ui

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestManagedFormDirtyBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed form dirty behavior regression test")
	}
	var fixtures []map[string]any
	for _, tc := range []struct {
		name, code, value, stored            string
		newRecord, dirty, noWrite, wantError bool
		file                                 bool
	}{
		{name: "save", code: `Объект.Записать();`, value: "saved", stored: "saved"},
		{name: "file_save", code: `Объект.Записать();`, value: "/old/path.csv", stored: "/old/path.csv", file: true},
		{name: "save_then_edit", code: `Объект.Записать(); Объект.Наименование = "unsaved";`, value: "unsaved", stored: "saved", dirty: true},
		{name: "save_then_edit_error", code: `Объект.Записать(); Объект.Наименование = "unsaved"; ВызватьИсключение "after write";`, value: "unsaved", stored: "saved", dirty: true, wantError: true},
		{name: "save_then_error", code: `Объект.Записать(); ВызватьИсключение "after write";`, value: "saved", stored: "saved", wantError: true},
		{name: "last_save_wins", code: `Объект.Записать(); Объект.Наименование = "unsaved"; Объект.Записать();`, value: "unsaved", stored: "unsaved"},
		{name: "edit_reverted", code: `Объект.Записать(); Объект.Наименование = "unsaved"; Объект.Наименование = "saved";`, value: "saved", stored: "saved"},
		{name: "new_save", code: `Объект.Записать();`, value: "saved", stored: "saved", newRecord: true},
		{name: "new_save_then_edit", code: `Объект.Записать(); Объект.Наименование = "unsaved";`, value: "unsaved", stored: "saved", newRecord: true, dirty: true},
		{name: "new_save_then_edit_error", code: `Объект.Записать(); Объект.Наименование = "unsaved"; ВызватьИсключение "after write";`, value: "unsaved", stored: "saved", newRecord: true, dirty: true, wantError: true},
		{name: "no_write", code: `Сообщить("no write");`, value: "saved", stored: "ОБР-000002", noWrite: true},
		{name: "rollback", code: `НачатьТранзакцию(); Объект.Записать(); ОтменитьТранзакцию();`, value: "saved", stored: "ОБР-000002", noWrite: true},
		{name: "rollback_after_prior_save", code: `Объект.Записать(); НачатьТранзакцию(); Объект.Наименование = "unsaved"; Объект.Записать(); ОтменитьТранзакцию();`, value: "unsaved", stored: "saved", dirty: true},
		{name: "row_edited_after_save", code: `Стр = Объект.Строки.Добавить(); Стр.Канал = "saved"; Объект.Записать(); Стр.Канал = "unsaved";`, value: "saved", stored: "saved", dirty: true},
		{name: "row_saved_again", code: `Стр = Объект.Строки.Добавить(); Стр.Канал = "saved"; Объект.Записать(); Стр.Канал = "unsaved"; Объект.Записать();`, value: "saved", stored: "saved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setupFormCtxServer(t, "Процедура Тест()\n"+tc.code+"\nКонецПроцедуры", nil)
			fieldType, input := "", "saved"
			if tc.file {
				fieldType, input = "file", "/old/path.csv"
			}
			f.entity.Forms[0].Elements = append(f.entity.Forms[0].Elements, &metadata.FormElement{
				Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование", Type: fieldType,
			})
			body := url.Values{"Наименование": {input}, "_element": {"КнопкаТест"}, "_event": {"Нажатие"}, "_kind": {"object"}}
			if !tc.newRecord {
				body.Set("_id", f.docID.String())
			}
			rec := executeFormEvent(t, f.srv, f.entity, body)
			resp := decodeFormEventResponse(t, rec.Body.Bytes())
			if rec.Code != 200 || resp.OK == tc.wantError || (resp.Error != "") != tc.wantError {
				t.Fatalf("unexpected response: %s", rec.Body.String())
			}
			if resp.Values["Наименование"] != tc.value || (resp.Version == 0) != tc.noWrite || (resp.SavedID != "") != tc.newRecord {
				t.Fatalf("values/version/identity changed: %s", rec.Body.String())
			}
			id := f.docID
			if tc.newRecord {
				id = uuid.MustParse(resp.SavedID)
			}
			row, err := f.srv.store.GetByID(context.Background(), f.entity.Name, id, f.entity)
			if err != nil || row["Наименование"] != tc.stored {
				t.Fatalf("persisted value: %v, err=%v; want %q", row, err, tc.stored)
			}
			fixtures = append(fixtures, map[string]any{
				"name": tc.name, "response": json.RawMessage(rec.Body.Bytes()), "id": body.Get("_id"),
				"dirty": tc.dirty, "noWrite": tc.noWrite, "value": tc.value,
				"input": input, "file": tc.file,
			})
		})
	}
	if t.Failed() {
		return
	}
	payload, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join(t.TempDir(), "form-events.json")
	if err := os.WriteFile(fixturePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--test", "static/managed_form_dirty_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = append(os.Environ(), "ONEBASE_FORM_DIRTY_FIXTURES="+fixturePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node managed form dirty behavior test: %v\n%s", err, output)
	}
}
