package ui

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
)

// Поиск уходит отдельным запросом вне очереди записывающих form-event.
// Поэтому любая попытка записи из его обработчика должна завершиться ошибкой
// до commit, даже если запись сделана через другой справочник.
func TestPicker_ServerSearchCannotWriteThroughPublicFormEvent(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Зап = Справочники.Направление.Создать();
	Зап.Наименование = "ЗАПИСЬ_ИЗ_ПОИСКА";
	Зап.Записать();
КонецПроцедуры
`, nil)
	f.entity.Forms[0].Elements[0].Handlers[metadata.FormEventOnSearch] = "Тест"
	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("_element", "КнопкаТест")
	body.Set("_event", string(metadata.FormEventOnSearch))
	body.Set("_pick_query", "фрагмент")
	resp := decodeFormEventResponse(t, executeFormEvent(t, f.srv, f.entity, body).Body.Bytes())
	if resp.OK || !strings.Contains(resp.Error, "запись недоступна в обработчике Поиск") {
		t.Fatalf("поиск разрешил запись: ok=%v error=%q", resp.OK, resp.Error)
	}
	var count int
	if err := f.srv.store.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM направление WHERE наименование = ?`, "ЗАПИСЬ_ИЗ_ПОИСКА").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("поиск записал %d строк", count)
	}
}

func TestPicker_ServerSearchCannotStartScheduledJob(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	РегламентныеЗадания.Запустить("Proof");
КонецПроцедуры
`, nil)
	f.entity.Forms[0].Elements[0].Handlers[metadata.FormEventOnSearch] = "Тест"
	if err := f.srv.store.EnsureScheduledRunsTable(context.Background()); err != nil {
		t.Fatal(err)
	}
	sched := scheduler.New(f.srv.store, f.srv.reg, f.srv.interp)
	if err := sched.RegisterGoJob("Proof", "Proof", "@every 100h", func(context.Context) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.srv.sched = sched
	t.Cleanup(func() { _ = sched.Shutdown(context.Background()) })

	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("_element", "КнопкаТест")
	body.Set("_event", string(metadata.FormEventOnSearch))
	resp := decodeFormEventResponse(t, executeFormEvent(t, f.srv, f.entity, body).Body.Bytes())
	if resp.OK || !strings.Contains(resp.Error, "запись недоступна в обработчике Поиск") {
		t.Fatalf("поиск запустил задание: ok=%v error=%q", resp.OK, resp.Error)
	}
	runs, err := f.srv.store.ScheduledRuns(context.Background(), "Proof", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("поиск создал прогоны задания: %+v", runs)
	}
}

func TestPicker_ServerSearchCannotWriteFormObject(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Объект.Наименование = "ЗАПИСЬ_ИЗ_ПОИСКА";
	Объект.Записать();
КонецПроцедуры
`, nil)
	f.entity.Forms[0].Elements[0].Handlers[metadata.FormEventOnSearch] = "Тест"
	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("_element", "КнопкаТест")
	body.Set("_event", string(metadata.FormEventOnSearch))
	resp := decodeFormEventResponse(t, executeFormEvent(t, f.srv, f.entity, body).Body.Bytes())
	if resp.OK || !strings.Contains(resp.Error, "запись недоступна в обработчике Поиск") {
		t.Fatalf("поиск разрешил Объект.Записать: ok=%v error=%q", resp.OK, resp.Error)
	}
	row, err := f.srv.store.GetByID(context.Background(), f.entity.Name, f.docID, f.entity)
	if err != nil {
		t.Fatal(err)
	}
	if row["Наименование"] == "ЗАПИСЬ_ИЗ_ПОИСКА" {
		t.Fatal("поиск сохранил изменения объекта")
	}
}

func TestPicker_ServerSearchCannotWriteFromProcessorForm(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "КнопкаПодбор",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnSearch: "ПоискПодбора"},
	})
	form.ProgramAST = mustParse(t, `
Процедура ПоискПодбора()
	Зап = Справочники.Проба.Создать();
	Зап.Наименование = "ЗАПИСЬ_ИЗ_ПОИСКА";
	Зап.Записать();
КонецПроцедуры
`)
	proc := &processor.Processor{Name: "ПробаПоиска", Forms: []*metadata.FormModule{form}}
	srv, db := newProcessorFormEventExecutionServer(t, proc, nil)
	ent := &metadata.Entity{Name: "Проба", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	if err := db.Migrate(context.Background(), []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	srv.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	body := url.Values{}
	body.Set("_element", "КнопкаПодбор")
	body.Set("_event", string(metadata.FormEventOnSearch))
	body.Set("_pick_query", "фрагмент")
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.OK || !strings.Contains(resp.Error, "запись недоступна в обработчике Поиск") {
		t.Fatalf("обработка разрешила запись: ok=%v error=%q", resp.OK, resp.Error)
	}
	var count int
	if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM проба`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("поиск обработки записал %d строк", count)
	}
}

func TestPicker_ServerSearchStillReadsCatalog(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Реф = Справочники.Направление.НайтиПоРеквизиту("Код", "REM");
	Сообщить(Реф.Наименование);
КонецПроцедуры
`, nil)
	f.entity.Forms[0].Elements[0].Handlers[metadata.FormEventOnSearch] = "Тест"
	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("_element", "КнопкаТест")
	body.Set("_event", string(metadata.FormEventOnSearch))
	resp := decodeFormEventResponse(t, executeFormEvent(t, f.srv, f.entity, body).Body.Bytes())
	if !resp.OK || len(resp.Messages) != 1 || resp.Messages[0] != "Ремонт" {
		t.Fatalf("поиск не смог читать справочник: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
	}
}
