package ui

// Дата документа в управляемой форме: открытие без правок не делает форму
// «изменённой», а запись не отрезает секунды.
//
// Поле даты рисовалось с точностью до минуты («2026-09-30T09:16»). Отсюда два
// дефекта одного корня. Первый — тихая порча данных: «Записать» сохранял то, что
// держит поле, и документ от 09:16:11 становился документом от 09:16:00, а внутри
// минуты менялся порядок документов (МоментВремени, ФИФО, остатки на момент).
// Второй — ложное «изменено»: ответ на ПриОткрытии сравнивал дату формы с
// базой и, не совпав в секундах (а на SQLite ещё и в зоне), возвращал dirty=true.
// У проведённого документа к этому добавлялся признак проведения: обычное
// событие формы его не дочитывает, а сверка сравнивала его с базой. Любой
// документ с обработчиком ПриОткрытии открывался со звёздочкой в заголовке и
// спрашивал «Данные изменены и не записаны» при закрытии без единой правки.
//
// Путь публичный, как у браузера: страница формы → значение поля → событие
// ПриОткрытии → запись формы. Матрица — потому что дату SQLite хранит TEXT в
// UTC, а PostgreSQL отдаёт TIMESTAMPTZ в зоне сессии.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func fds1Doc(t *testing.T) *metadata.Entity {
	t.Helper()
	doc := &metadata.Entity{
		Name:    "ДатаССекундами" + strings.ReplaceAll(uuid.NewString()[:8], "-", ""),
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields: []metadata.Field{
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
		TableParts: []metadata.TablePart{{
			Name: "Товары",
			Fields: []metadata.Field{
				{Name: "Цена", Type: metadata.FieldTypeNumber},
				{Name: "ДатаПоставки", Type: metadata.FieldTypeDate},
			},
		}},
	}
	// Обработчик как в прикладных формах: заполняет только пустое, поэтому у
	// существующего документа ничего не меняет.
	doc.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: doc.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Handlers:   map[metadata.FormEventType]string{metadata.FormEventOnOpen: "ПриОткрытииФормы"},
		ProgramAST: mustParse(t, `
Процедура ПриОткрытииФормы()
    Если НЕ ЗначениеЗаполнено(Объект.Дата) Тогда
        Объект.Дата = ТекущаяДатаВремя();
    КонецЕсли;
КонецПроцедуры
`),
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеДата", DataPath: "Объект.Дата"},
			{Kind: metadata.FormElementField, Name: "ПолеСумма", DataPath: "Объект.Сумма"},
			{Kind: metadata.FormElementTablePart, Name: "Товары", DataPath: "Объект.Товары"},
		},
	}}
	return doc
}

var fds1InputValue = regexp.MustCompile(`name="Дата" value="([^"]*)"`)

func TestManagedDocumentFormKeepsDateSeconds(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("MSK", 3*60*60)
	t.Cleanup(func() { time.Local = saved })

	for _, posted := range []bool{false, true} {
		name := "черновик"
		if posted {
			name = "проведённый"
		}
		t.Run(name, func(t *testing.T) { fds1Check(t, posted) })
	}
}

func fds1Check(t *testing.T, posted bool) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := fds1Doc(t)
		ts, _ := tpz1136Server(t, db, []*metadata.Entity{doc})
		id := uuid.New()
		original := time.Date(2026, 9, 30, 6, 16, 11, 0, time.UTC)
		delivery := time.Date(2026, 10, 2, 7, 45, 30, 0, time.UTC)
		if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Дата": original, "Сумма": 100}, doc); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if err := db.UpsertTablePartRows(ctx, doc.Name, "Товары", id,
			[]map[string]any{{"Цена": 5, "ДатаПоставки": delivery}}, doc.TableParts[0]); err != nil {
			t.Fatalf("UpsertTablePartRows: %v", err)
		}
		if posted {
			if err := db.SetPosted(ctx, doc.Name, id, true); err != nil {
				t.Fatalf("SetPosted: %v", err)
			}
		}
		base := ts.URL + "/ui/document/" + url.PathEscape(doc.Name)

		// Страница формы: какое значение получает поле даты.
		resp, err := http.Get(base + "/" + id.String()) //nolint:noctx // локальный httptest.Server
		if err != nil {
			t.Fatalf("GET формы: %v", err)
		}
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close() //nolint:errcheck,gosec // тело уже прочитано
		m := fds1InputValue.FindSubmatch(page)
		if m == nil {
			t.Fatalf("поле Дата не найдено на странице формы")
		}
		shown := string(m[1])
		if shown != "2026-09-30T09:16:11" {
			t.Errorf("поле даты = %q, ожидалось местное время с секундами %q", shown, "2026-09-30T09:16:11")
		}
		// Строка ТЧ уходит обратно тем, что грид получил от сервера.
		rows := []map[string]any{{"Цена": "5", "ДатаПоставки": serializeValue(delivery)}}
		rowsJSON, _ := json.Marshal(rows)
		values := url.Values{
			"_version":       {"1"},
			"Дата":           {shown},
			"Сумма":          {"100"},
			"tp_json.Товары": {string(rowsJSON)},
		}

		// ПриОткрытии без правок: форма не «изменена».
		event := url.Values{}
		for k, v := range values {
			event[k] = v
		}
		event.Set("_event", string(metadata.FormEventOnOpen))
		event.Set("_element", "")
		event.Set("_kind", "object")
		event.Set("_id", id.String())
		resp, err = http.PostForm(base+"/form-event", event) //nolint:noctx // локальный httptest.Server
		if err != nil {
			t.Fatalf("POST form-event: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close() //nolint:errcheck,gosec // тело уже прочитано
		var reply formEventResponse
		if err := json.Unmarshal(body, &reply); err != nil {
			t.Fatalf("ответ form-event: %v; %s", err, body)
		}
		if !reply.OK || reply.Dirty == nil || *reply.Dirty {
			t.Errorf("ПриОткрытии без правок: ожидалась чистая форма (ok=true, dirty=false); ответ %s", body)
		}

		// «Записать» без правок: дата документа и строки не меняются.
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err = client.PostForm(base+"/"+id.String(), values) //nolint:noctx // локальный httptest.Server
		if err != nil {
			t.Fatalf("POST записи: %v", err)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close() //nolint:errcheck,gosec // тело уже прочитано
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("запись формы: ответ %d, ожидался 303; %s", resp.StatusCode, body)
		}
		stored, err := db.GetByID(ctx, doc.Name, id, doc)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got := fds1Instant(t, stored["Дата"]); !got.Equal(original) {
			t.Errorf("дата после записи формой = %s, ожидалась исходная %s", got.UTC().Format(time.RFC3339), original.Format(time.RFC3339))
		}
		storedRows, err := db.GetTablePartRows(ctx, doc.Name, "Товары", id, doc.TableParts[0])
		if err != nil || len(storedRows) != 1 {
			t.Fatalf("строки ТЧ после записи: %v, %v", storedRows, err)
		}
		if got := fds1Instant(t, storedRows[0]["ДатаПоставки"]); !got.Equal(delivery) {
			t.Errorf("дата в строке ТЧ после записи = %s, ожидалась исходная %s", got.UTC().Format(time.RFC3339), delivery.Format(time.RFC3339))
		}
	})
}

func fds1Instant(t *testing.T, v any) time.Time {
	t.Helper()
	switch d := v.(type) {
	case time.Time:
		return d
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05-07:00", "2006-01-02T15:04:05"} {
			if parsed, err := time.Parse(layout, d); err == nil {
				return parsed
			}
		}
	}
	t.Fatalf("значение даты %T %v не разобрать", v, v)
	return time.Time{}
}
