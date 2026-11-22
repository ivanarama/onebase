package ui

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/i18n"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Отказ ПриЧтенииНаСервере показывает пользователю текст ВызватьИсключение —
// «почему нельзя», как Предупреждение в 1С, — а не только общее «нет прав».
// Данные записи при этом по-прежнему не раскрываются.
func TestFormEdit_OnReadAtServerDenialShowsExceptionText(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьДоступ()
	ВызватьИсключение("Запрещен просмотр карточки для должности: Менеджер направления колл центр");
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventType("ПриЧтенииНаСервере"): "ПроверитьДоступ",
	}, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
	})
	id := insertContragent(t, srv, ent, "СЕКРЕТНЫЙ-КОНТРАГЕНТ")
	rec := executeFormEditGET(t, srv, ent, id)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("ожидался 403, получен %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Запрещен просмотр карточки для должности: Менеджер направления колл центр") {
		t.Errorf("страница отказа не показала текст исключения:\n%s", body)
	}
	if strings.Contains(body, "СЕКРЕТНЫЙ-КОНТРАГЕНТ") {
		t.Error("при отказе данные записи раскрыты")
	}
	if strings.Contains(body, ".os:") {
		t.Error("пользователю показан путь модуля и номер строки")
	}
}

func TestReadHookDenied_TechnicalFailuresStayGeneric(t *testing.T) {
	for _, body := range []string{
		`x = Число("PRIVATE-CONVERSION-TOKEN");`,
		`Попытка x = Число("PRIVATE-CONVERSION-TOKEN"); Исключение ВызватьИсключение; КонецПопытки;`,
		`q = Новый Запрос("ВЫБРАТЬ PRIVATE_SQL_TOKEN ИЗ PRIVATE_MISSING_TABLE"); q.Выполнить();`,
		`Попытка q = Новый Запрос("ВЫБРАТЬ PRIVATE_SQL_TOKEN ИЗ PRIVATE_MISSING_TABLE"); q.Выполнить(); Исключение ВызватьИсключение; КонецПопытки;`,
	} {
		for _, panel := range []bool{false, true} {
			t.Run(body+fmt.Sprint(panel), func(t *testing.T) {
				srv, ent := setupManagedEventsServer(t, "Процедура ПроверитьДоступ()\n"+body+"\nКонецПроцедуры", map[metadata.FormEventType]string{
					metadata.FormEventType("ПриЧтенииНаСервере"): "ПроверитьДоступ",
				}, []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"}})
				id := insertContragent(t, srv, ent, "PRIVATE-RECORD-DATA")
				target := "/ui/catalog/" + ent.Name + "/" + id.String()
				if panel {
					target += "/detail-panel"
				}
				req := reqWithChi(http.MethodGet, target, nil, map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()})
				rec := httptest.NewRecorder()
				if panel {
					srv.detailPanelRecord(rec, req)
				} else {
					srv.formEdit(rec, req)
				}
				generic := httptest.NewRecorder()
				srv.renderForbidden(generic, req)
				if rec.Code != http.StatusForbidden || rec.Body.String() != generic.Body.String() {
					t.Fatalf("technical failure must use generic 403: status=%d body=%s", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// Use real module paths: a <test> source cannot catch file:line disclosure.
// Both HTTP entry points must keep the localized reason and HTML escaping.
func TestReadHookDenied_OmitsSourceAndLocalizes(t *testing.T) {
	const message = "Нет доступа к карточке <закрыто>"
	const translated = "Card access denied <closed>"
	bundle, err := i18n.Load(fstest.MapFS{
		"en.json": &fstest.MapFile{Data: []byte(`{"Нет доступа к карточке <закрыто>":"Card access denied <closed>"}`)},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, modulePath := range []string{
		"/srv/private/config/forms/контрагент/объект.form.os",
		`C:\private\config\forms\контрагент\объект.form.os`,
	} {
		for _, lang := range []struct {
			name   string
			bundle *i18n.Bundle
			lang   string
			want   string
		}{
			{name: "no-bundle", want: message},
			{name: "ru", bundle: bundle, lang: "ru", want: message},
			{name: "en", bundle: bundle, lang: "en", want: translated},
		} {
			for _, panel := range []bool{false, true} {
				endpoint := "card"
				if panel {
					endpoint = "detail-panel"
				}
				t.Run(modulePath+"/"+lang.name+"/"+endpoint, func(t *testing.T) {
					source := "Процедура ПроверитьДоступ()\nВызватьИсключение(\"" + message + "\");\nКонецПроцедуры"
					program, err := parser.New(lexer.New(source, modulePath)).ParseProgram()
					if err != nil {
						t.Fatal(err)
					}
					srv, ent := setupManagedEventsServer(t, "", map[metadata.FormEventType]string{
						metadata.FormEventType("ПриЧтенииНаСервере"): "ПроверитьДоступ",
					}, []*metadata.FormElement{
						{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
					})
					ent.Forms[0].ProgramAST = program
					srv.cfg.Bundle, srv.cfg.Lang = lang.bundle, lang.lang
					id := insertContragent(t, srv, ent, "СЕКРЕТНЫЙ-КОНТРАГЕНТ")
					target := "/ui/catalog/" + ent.Name + "/" + id.String()
					if panel {
						target += "/detail-panel"
					}
					req := reqWithChi(http.MethodGet, target, nil, map[string]string{
						"kind": "catalog", "entity": ent.Name, "id": id.String(),
					})
					rec := httptest.NewRecorder()
					if panel {
						srv.detailPanelRecord(rec, req)
					} else {
						srv.formEdit(rec, req)
					}
					if rec.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want 403", rec.Code)
					}
					body := rec.Body.String()
					if !strings.Contains(body, html.EscapeString(lang.want)) {
						t.Errorf("missing escaped localized reason %q", lang.want)
					}
					for _, secret := range []string{modulePath, "private", "объект.form.os", ".os:2", "СЕКРЕТНЫЙ-КОНТРАГЕНТ", "<закрыто>", "<closed>"} {
						if strings.Contains(body, secret) {
							t.Errorf("response disclosed source/data or unescaped markup %q", secret)
						}
					}
				})
			}
		}
	}
}
