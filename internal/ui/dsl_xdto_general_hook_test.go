package ui

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func xdtoGeneralProject(t *testing.T, kind, hook, processor string) (*project.Project, *metadata.Entity) {
	t.Helper()
	dir := t.TempDir()
	path, name, header := "catalogs/кат.yaml", "Кат", ""
	if kind == "document" {
		path, name, header = "documents/док.yaml", "Док", "posting: true\n"
	}
	files := map[string]string{
		path: "name: " + name + "\n" + header + `fields:
  - {name: Наименование, type: string}
  - {name: Номер, type: string}
  - {name: Дата, type: date}
  - {name: Телефон, type: string}
  - {name: Отказать, type: bool}
  - {name: Экспорт, type: bool}
  - {name: Снимок, type: string}
  - {name: Круг, type: string}
tableparts:
  - name: Товары
    fields:
      - {name: Артикул, type: string}
      - {name: Количество, type: number}
`,
		"src/" + strings.ToLower(name) + ".os": hook,
		"processors/проба.yaml":                "name: Проба\n",
		"src/проба.proc.os":                    processor,
	}
	for name, text := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proj.Close)
	return proj, proj.Entities[0]
}

func xdtoGeneralUser(kind, policy string) *auth.User {
	user := uiMaskUser([]string{"read", "write", "post"}, nil)
	var fields auth.FieldPolicies
	switch policy {
	case "mask_tail":
		fields = auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}}
	case "hide":
		fields = auth.FieldPolicies{"Телефон": {Read: "hide"}}
	}
	if kind == "document" {
		user.Roles[0].Permissions.Documents = map[string][]string{"Док": {"read", "write", "post"}}
		user.Roles[0].Permissions.FieldAccess.Documents = map[string]auth.FieldPolicies{"Док": fields}
	} else {
		user.Roles[0].Permissions.Catalogs = map[string][]string{"Кат": {"read", "write"}}
		user.Roles[0].Permissions.FieldAccess.Catalogs = map[string]auth.FieldPolicies{"Кат": fields}
	}
	return user
}

func xdtoGeneralPhone(policy string) string {
	switch policy {
	case "mask_tail":
		return "••••••••4455"
	case "hide":
		return ""
	default:
		return "+79161234455"
	}
}

func assertXDTOGeneralSnapshot(t *testing.T, text string, id uuid.UUID, phone string, posted, marked bool, document bool) {
	t.Helper()
	var snapshot struct {
		Ref          string
		Posted       *bool
		DeletionMark *bool
		Phone        string `xml:"Телефон"`
	}
	if err := xml.Unmarshal([]byte(text), &snapshot); err != nil {
		t.Fatalf("XML: %v: %s", err, text)
	}
	if snapshot.Ref != id.String() || snapshot.DeletionMark == nil || *snapshot.DeletionMark != marked || snapshot.Phone != phone {
		t.Fatalf("identity/policy/state mismatch: %s", text)
	}
	if document && (snapshot.Posted == nil || *snapshot.Posted != posted) {
		t.Fatalf("Posted, want %t: %s", posted, text)
	}
	for _, want := range []string{"<Товары>", "<Артикул>SKU-1</Артикул>", "<Количество>2</Количество>"} {
		if !strings.Contains(text, want) {
			t.Fatalf("snapshot lost %s: %s", want, text)
		}
	}
}

// The form/REST entry point must export the same in-flight object in both
// OnWrite and OnPost. This also checks assignment propagation between phases.
func TestXDTOGeneralSavePolicyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, policy := range []string{"full", "mask_tail", "hide"} {
			for _, source := range []string{"new", "loaded", "submitted_patch", "assigned_in_write", "assigned_in_post", "indexed_in_post", "control"} {
				t.Run(policy+"/"+source, func(t *testing.T) {
					writeAssign, postAssign := "", ""
					if source == "assigned_in_write" {
						writeAssign = `ЭтотОбъект.Телефон = "+79990001122";`
					}
					if source == "assigned_in_post" {
						postAssign = `ЭтотОбъект.Телефон = "+79990001122";`
					}
					if source == "indexed_in_post" {
						postAssign = `ЭтотОбъект["Телефон"] = "+79990001122";`
					}
					export := `Если ЭтотОбъект.Экспорт Тогда
 XML = СериализаторXDTO.ЗаписатьXML(ЭтотОбъект);
 Сообщить(XML);
 Сообщить(СериализаторXDTO.ЗаписатьXML(СериализаторXDTO.ПрочитатьXML(XML)));
 КонецЕсли;`
					hook := "Процедура ПриЗаписи()\n" + writeAssign + export + "\nКонецПроцедуры\nПроцедура ОбработкаПроведения()\n" + postAssign + export + "\nКонецПроцедуры"
					proj, entity := xdtoGeneralProject(t, "document", hook, "Процедура Выполнить() КонецПроцедуры")
					ctx := context.Background()
					if err := db.Migrate(ctx, proj.Entities); err != nil {
						t.Fatal(err)
					}
					s, _, err := NewOfflineServer(proj, db)
					if err != nil {
						t.Fatal(err)
					}
					id := uuid.New()
					fields := map[string]any{"Номер": id.String(), "Дата": time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local), "Телефон": "+79161234455", "Экспорт": source != "control"}
					parts := map[string][]map[string]any{"Товары": {{"Артикул": "SKU-1", "Количество": float64(2)}}}
					isNew := source == "new"
					if !isNew {
						if err := db.Upsert(ctx, entity.Name, id, fields, entity); err != nil {
							t.Fatal(err)
						}
					}
					if source == "submitted_patch" {
						fields["Телефон"] = "+79990001122"
					}
					result, err := s.EntitySvc().Save(auth.ContextWithUser(ctx, xdtoGeneralUser("document", policy)), entityservice.SaveRequest{
						Entity: entity, ID: id, IsNew: isNew, Fields: fields, TablePartRows: parts, Action: "post",
					})
					if err != nil || result.DSLError != "" {
						t.Fatalf("Save: %v / %s / %q", err, result.DSLError, result.DSLMessages)
					}
					if source == "control" {
						if len(result.DSLMessages) != 0 {
							t.Fatal(result.DSLMessages)
						}
						return
					}
					if len(result.DSLMessages) != 4 {
						t.Fatalf("want two hooks and round-trips: %q", result.DSLMessages)
					}
					for phase := 0; phase < 2; phase++ {
						phone := xdtoGeneralPhone(policy)
						if isNew {
							phone = "+79161234455"
						}
						if source == "submitted_patch" {
							phone = "+79990001122"
							switch policy {
							case "hide":
								phone = ""
							case "mask_tail":
								phone = "••••••••1122"
							}
						}
						if writeAssign != "" || phase == 1 && postAssign != "" {
							phone = "+79990001122"
						}
						messages := result.DSLMessages[phase*2 : phase*2+2]
						if messages[0] != messages[1] {
							t.Fatal("round-trip changed XML", messages)
						}
						assertXDTOGeneralSnapshot(t, messages[0], id, phone, false, false, true)
						if !strings.Contains(messages[0], "<Number>"+id.String()+"</Number>") || !strings.Contains(messages[0], "<Date>2026-01-02T03:04:05</Date>") {
							t.Fatal("header lost", messages[0])
						}
					}
				})
			}
		}
	})
}

func TestXDTOGeneralSaveStateMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, tc := range []struct {
			name                            string
			isNew, posted, rollback, reject bool
		}{
			{"new", true, false, false, false},
			{"saved", false, false, false, false},
			{"repost", false, true, false, false},
			{"rollback_new", true, false, true, false},
			{"rollback_saved", false, false, true, false},
			{"rollback_repost", false, true, true, false},
			{"rejected_new", true, false, false, true},
			{"rejected_saved", false, false, false, true},
			{"rejected_repost", false, true, false, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				proj, entity := xdtoGeneralProject(t, "document", `Процедура ОбработкаПроведения()
 Сообщить(СериализаторXDTO.ЗаписатьXML(ЭтотОбъект));
 Если ЭтотОбъект.Отказать Тогда ВызватьИсключение "ожидаемый отказ"; КонецЕсли;
КонецПроцедуры`, "Процедура Выполнить() КонецПроцедуры")
				ctx := context.Background()
				if err := db.Migrate(ctx, proj.Entities); err != nil {
					t.Fatal(err)
				}
				s, _, err := NewOfflineServer(proj, db)
				if err != nil {
					t.Fatal(err)
				}
				id := uuid.New()
				fields := map[string]any{"Номер": id.String(), "Телефон": "+79161234455", "Отказать": tc.reject}
				if !tc.isNew {
					if err := db.Upsert(ctx, entity.Name, id, fields, entity); err != nil {
						t.Fatal(err)
					}
					if err := db.SetPosted(ctx, entity.Name, id, tc.posted); err != nil {
						t.Fatal(err)
					}
				}
				var obj *runtime.Object
				var result entityservice.SaveResult
				rollback := errors.New("explicit rollback")
				err = db.WithTxScope(ctx, func(txCtx context.Context) error {
					var saveErr error
					result, saveErr = s.EntitySvc().Save(txCtx, entityservice.SaveRequest{
						Entity: entity, ID: id, IsNew: tc.isNew, Fields: fields, Action: "post",
						TablePartRows: map[string][]map[string]any{"Товары": {{"Артикул": "SKU-1", "Количество": float64(2)}}},
						Preflight:     func(_ context.Context, o *runtime.Object) error { obj = o; return nil },
					})
					if saveErr != nil {
						return saveErr
					}
					if (result.DSLError != "") != tc.reject {
						t.Fatalf("Save: %s", result.DSLError)
					}
					if obj.Posted != (tc.posted || !tc.reject) {
						t.Fatalf("in-flight Posted=%t", obj.Posted)
					}
					if tc.rollback {
						return rollback
					}
					return nil
				})
				if err != nil && !errors.Is(err, rollback) {
					t.Fatal(err)
				}
				if len(result.DSLMessages) != 1 {
					t.Fatalf("hook: %q / %s", result.DSLMessages, result.DSLError)
				}
				assertXDTOGeneralSnapshot(t, result.DSLMessages[0], id, "+79161234455", tc.posted, false, true)
				row, err := db.GetByID(ctx, entity.Name, id, entity)
				if tc.isNew && (tc.rollback || tc.reject) {
					if err == nil {
						t.Fatalf("rolled-back row exists: %v", row)
					}
				} else if err != nil || asBool(row["posted"]) != (tc.posted || !tc.rollback && !tc.reject) {
					t.Fatalf("durable state: %v / %v", row, err)
				}
				if obj.Posted != (tc.posted || !tc.rollback && !tc.reject) {
					t.Fatalf("object state after rollback: %+v", obj)
				}
			})
		}
	})
}

func TestXDTOGeneralCatalogPolicyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, policy := range []string{"full", "mask_tail", "hide"} {
			for _, source := range []string{"new", "saved_by_module", "loaded", "assigned_before_write", "indexed_before_write", "assigned_in_hook", "indexed_in_hook", "rollback_new", "rollback_loaded", "reject_new", "reject_loaded", "marked", "control"} {
				t.Run(policy+"/"+source, func(t *testing.T) {
					assignment := ""
					if source == "assigned_in_hook" {
						assignment = `ЭтотОбъект.Телефон = "+79990001122";`
					}
					if source == "indexed_in_hook" {
						assignment = `ЭтотОбъект["Телефон"] = "+79990001122";`
					}
					create := `Об = Справочники.Кат.Создать(); Об.Наименование = "полная шапка"; Об.Телефон = "+79161234455";
 Стр = Об.Товары.Добавить(); Стр.Артикул = "SKU-1"; Стр.Количество = 2;`
					setup := create
					isNew := source == "new" || source == "rollback_new" || source == "reject_new" || source == "control"
					if source == "saved_by_module" {
						setup += "Об.Записать();"
					}
					id := uuid.New()
					if !isNew && source != "saved_by_module" {
						setup = fmt.Sprintf(`Об = Справочники.Кат.НайтиПоИдентификатору("%s").ПолучитьОбъект();`, id)
					}
					if source == "assigned_before_write" {
						setup += `Об.Телефон = "+79990001122";`
					}
					if source == "indexed_before_write" {
						setup += `Об["Телефон"] = "+79990001122";`
					}
					setup += `Об.Экспорт = Истина;`
					if source == "control" {
						setup += `Об.Экспорт = Ложь;`
					}
					if strings.HasPrefix(source, "reject_") {
						setup += `Об.Отказать = Истина;`
					}
					operation := "Об.Записать();"
					if strings.HasPrefix(source, "rollback_") {
						operation = "НачатьТранзакцию(); Об.Записать(); ОтменитьТранзакцию();"
					}
					if strings.HasPrefix(source, "reject_") {
						operation = `БылОтказ = Ложь;
 Попытка Об.Записать(); Исключение БылОтказ = Истина; КонецПопытки;
 Если Не БылОтказ Тогда ВызватьИсключение "не было отказа"; КонецЕсли;`
					}
					hook := "Процедура ПриЗаписи()\n" + assignment + `
 Если ЭтотОбъект.Экспорт Тогда
 XML = СериализаторXDTO.ЗаписатьXML(ЭтотОбъект); Сообщить(XML);
 ЭтотОбъект.Снимок = XML;
 ЭтотОбъект.Круг = СериализаторXDTO.ЗаписатьXML(СериализаторXDTO.ПрочитатьXML(XML));
 КонецЕсли;
 Если ЭтотОбъект.Отказать Тогда ВызватьИсключение "ожидаемый отказ"; КонецЕсли;
КонецПроцедуры`
					capture := `Сообщить(Об.Снимок); Сообщить(Об.Круг);`
					if source == "control" {
						capture = ""
					}
					proj, entity := xdtoGeneralProject(t, "catalog", hook, "Процедура Выполнить()\n"+setup+operation+capture+`Сообщить(СериализаторXDTO.ЗаписатьXML(Об)); КонецПроцедуры`)
					ctx := context.Background()
					if err := db.Migrate(ctx, proj.Entities); err != nil {
						t.Fatal(err)
					}
					if !isNew && source != "saved_by_module" {
						if err := db.Upsert(ctx, entity.Name, id, map[string]any{"Наименование": "полная шапка", "Телефон": "+79161234455"}, entity); err != nil {
							t.Fatal(err)
						}
						if err := db.UpsertTablePartRows(ctx, entity.Name, "Товары", id, []map[string]any{{"Артикул": "SKU-1", "Количество": float64(2)}}, entity.TableParts[0]); err != nil {
							t.Fatal(err)
						}
						if source == "marked" {
							if err := db.MarkForDeletion(ctx, entity.Name, id, true); err != nil {
								t.Fatal(err)
							}
						}
					}
					messages, runErr, err := RunProcessorOffline(auth.ContextWithUser(ctx, xdtoGeneralUser("catalog", policy)), proj, db, "Проба", nil, nil)
					if err != nil || runErr != nil {
						t.Fatalf("processor: %v / %v / %q", err, runErr, messages)
					}
					wantMessages := 3
					if source == "control" {
						wantMessages = 1
					}
					if len(messages) != wantMessages {
						t.Fatalf("snapshots: %q", messages)
					}
					if source == "control" {
						return
					}
					if messages[0] != messages[1] {
						t.Fatal("round-trip changed XML", messages)
					}
					var snap struct{ Ref string }
					if err := xml.Unmarshal([]byte(messages[0]), &snap); err != nil {
						t.Fatal(err)
					}
					if isNew || source == "saved_by_module" {
						id = uuid.MustParse(snap.Ref)
					}
					phone := xdtoGeneralPhone(policy)
					if isNew || source == "saved_by_module" {
						phone = "+79161234455"
					}
					if assignment != "" || (source == "assigned_before_write" || source == "indexed_before_write") && policy == "full" {
						phone = "+79990001122"
					}
					for _, text := range messages {
						assertXDTOGeneralSnapshot(t, text, id, phone, false, source == "marked", false)
						if !strings.Contains(text, "<Description>полная шапка</Description>") {
							t.Fatal("header lost", text)
						}
					}
					row, err := db.GetByID(ctx, entity.Name, id, entity)
					if isNew && (strings.HasPrefix(source, "rollback_") || strings.HasPrefix(source, "reject_")) {
						if err == nil {
							t.Fatalf("rolled-back row exists: %v", row)
						}
					} else {
						storedPhone := "+79161234455"
						if assignment != "" || (source == "assigned_before_write" || source == "indexed_before_write") && policy == "full" {
							storedPhone = "+79990001122"
						}
						if err != nil || row["Телефон"] != storedPhone {
							t.Fatalf("durable state: %v / %v", row, err)
						}
					}
				})
			}
		}
	})
}
