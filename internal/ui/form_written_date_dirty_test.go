package ui

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Признак «форма изменена» после события существующей записи сравнивает дату
// объекта с записью в базе с точностью хранения диалекта: SQLite пишет дату
// до секунды, PostgreSQL — до микросекунды.
func writtenDateServer(t *testing.T, db *storage.DB, handler string) (*Server, *metadata.Entity, uuid.UUID) {
	t.Helper()
	ent := &metadata.Entity{
		Name: "ЗаявкаСДатой", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ДатаРаспределения", Type: metadata.FieldTypeDate},
		},
	}
	form := managedObjectForm(&metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Команда",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Распределить"},
	})
	form.EntityName = ent.Name
	form.ProgramAST = mustParse(t, "Процедура Распределить()\n"+handler+"\nКонецПроцедуры\n")
	ent.Forms = []*metadata.FormModule{form}
	srv := newSubmitTestServerOn(t, db, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), ent.Name, id, map[string]any{
		"Наименование": "заявка", "ДатаРаспределения": time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
	}, ent); err != nil {
		t.Fatal(err)
	}
	return srv, ent, id
}

func writtenDateEvent(t *testing.T, srv *Server, ent *metadata.Entity, id uuid.UUID) formEventResponse {
	t.Helper()
	body := url.Values{
		"_element": {"Команда"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"},
		"_id": {id.String()}, "_version": {"1"}, "Наименование": {"заявка"},
	}
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if !resp.OK || resp.Dirty == nil {
		t.Fatalf("событие: %+v", resp)
	}
	return resp
}

// Обработчик ставит дату «сейчас» и записывает объект — после записи форма не
// «изменена». ТекущаяДатаВремя() несёт доли секунды, а SQLite хранит дату до
// секунды: сравнение до наносекунд видело разницу, и закрытие только что
// записанной формы спрашивало «Данные были изменены. Сохранить?»
// (конфигурация cc2, «Распределить» заявки).
func TestManagedFormEvent_WrittenCurrentDateIsNotDirty(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		srv, ent, id := writtenDateServer(t, db, `
	Объект.ДатаРаспределения = ТекущаяДатаВремя();
	Объект.Записать();`)
		resp := writtenDateEvent(t, srv, ent, id)
		if resp.Version == 0 {
			t.Fatalf("обработчик не записал объект: %+v", resp)
		}
		if *resp.Dirty {
			t.Fatalf("записанная форма с датой «сейчас» помечена изменённой: %+v", resp)
		}
	})
}

// Ревью #2174: несохранённый сдвиг даты на полсекунды — правка, которую
// PostgreSQL сохранил бы (12:00:00.5), поэтому форма «изменена». SQLite
// такую долю не хранит: запись ничего бы не изменила, и форма не изменена.
// Сдвиг на секунду изменён на обоих.
func TestManagedFormEvent_UnsavedDateShiftDirtyAtStoragePrecision(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, tc := range []struct {
			shift string
			want  map[string]bool
		}{
			{"0.5", map[string]bool{"postgres": true, "sqlite": false}},
			{"1", map[string]bool{"postgres": true, "sqlite": true}},
		} {
			t.Run("+"+tc.shift, func(t *testing.T) {
				srv, ent, id := writtenDateServer(t, db,
					"\tОбъект.ДатаРаспределения = Объект.ДатаРаспределения + "+tc.shift+";")
				resp := writtenDateEvent(t, srv, ent, id)
				if resp.Version != 0 {
					t.Fatalf("обработчик без Записать() записал объект: %+v", resp)
				}
				if want := tc.want[db.Dialect().Name()]; *resp.Dirty != want {
					t.Fatalf("dirty = %v, want %v (%s)", *resp.Dirty, want, db.Dialect().Name())
				}
			})
		}
	})
}

// Снимки в памяти сравниваются с полной точностью — приведение к точности
// хранения только для сравнения с базой.
func TestTPCellNormDatePrecision(t *testing.T) {
	f := metadata.Field{Name: "Д", Type: metadata.FieldTypeDate}
	a := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	b := a.Add(500 * time.Millisecond)
	if tpCellNorm(f, a) == tpCellNorm(f, b) {
		t.Fatal("снимки в памяти потеряли доли секунды")
	}
	if tpCellNormStored(f, a, "sqlite") != tpCellNormStored(f, b, "sqlite") {
		t.Fatal("SQLite хранит дату до секунды")
	}
	if tpCellNormStored(f, a, "postgres") == tpCellNormStored(f, b, "postgres") {
		t.Fatal("PostgreSQL хранит доли секунды")
	}
	if tpCellNormStored(f, a.Add(400*time.Nanosecond), "postgres") != tpCellNormStored(f, a, "postgres") {
		t.Fatal("PostgreSQL хранит до микросекунды")
	}
}
