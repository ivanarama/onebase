package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

// Состав колонок табличной части (план 154) проверяется тем же путём, которым
// его видит пользователь — GET карточки документа, а не вызовом планировщика
// колонок напрямую. До плана 154 чекбоксы состава в конструкторе писали детей
// kind: Колонка, а форма всё равно рисовала все реквизиты.
func tpColumnsFixture(t *testing.T, table *metadata.FormElement) (*Server, *metadata.Entity, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cols.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	order := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{
				{Name: "Количество", Type: metadata.FieldTypeNumber},
				{Name: "Цена", Type: metadata.FieldTypeNumber},
				{Name: "Сумма", Type: metadata.FieldTypeNumber},
			},
		}},
	}
	order.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: order.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements:   []*metadata.FormElement{table},
	}}

	if err := db.Migrate(ctx, []*metadata.Entity{order}); err != nil {
		t.Fatal(err)
	}
	orderID := uuid.New()
	if err := db.Upsert(ctx, order.Name, orderID, map[string]any{"Дата": time.Now()}, order); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTablePartRows(ctx, order.Name, "Строки", orderID, []map[string]any{
		{"Количество": 2, "Цена": 15, "Сумма": 30},
	}, order.TableParts[0]); err != nil {
		t.Fatal(err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{order}})
	return &Server{
		store:       db,
		reg:         reg,
		messages:    NewMessageStore(),
		widgetCache: widget.NewCache(time.Minute),
	}, order, orderID
}

func tpColumnsFormHTML(t *testing.T, s *Server, entity *metadata.Entity, id uuid.UUID) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/ui/document/"+entity.Name+"/"+id.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "document")
	rctx.URLParams.Add("entity", entity.Name)
	rctx.URLParams.Add("id", id.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{Login: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()
	s.formEdit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("карточка документа вернула %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func tpColumn(name, field string) *metadata.FormElement {
	return &metadata.FormElement{
		Kind: metadata.FormElementColumn, Name: name,
		DataPath: "Объект.Строки." + field,
	}
}

// Табличная часть без детей kind: Колонка показывает все реквизиты. Так живут
// все конфигурации, написанные до появления выбора состава: «ничего не
// выбрано» обязано значить «показать всё», а не «показать пусто».
func TestTPКолонки_БезВыбораПоказываютсяВсе(t *testing.T) {
	s, order, id := tpColumnsFixture(t, &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ТабСтроки", DataPath: "Объект.Строки",
	})
	cols := parseManagedTPColumns(t, tpColumnsFormHTML(t, s, order, id))

	if len(cols) != 3 {
		t.Fatalf("колонок %d, ожидалось 3: %+v", len(cols), cols)
	}
	want := []string{"Количество", "Цена", "Сумма"}
	for i, name := range want {
		if cols[i].ID != name {
			t.Errorf("колонка %d = %q, ожидалась %q", i, cols[i].ID, name)
		}
		if cols[i].Hidden {
			t.Errorf("колонка %q скрыта, хотя состав не выбирали", cols[i].ID)
		}
		if cols[i].Index != i {
			t.Errorf("колонка %q: index=%d, ожидался %d", cols[i].ID, cols[i].Index, i)
		}
	}
}

// Выбор задаёт и состав, и порядок. Невыбранный реквизит уходит в конец
// СКРЫТЫМ, а не выбрасывается: columnsMeta на клиенте собирается из этого же
// списка, и выпавшая колонка означала бы затирание реквизита при записи.
func TestTPКолонки_ВыборЗадаётСоставИПорядок(t *testing.T) {
	s, order, id := tpColumnsFixture(t, &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ТабСтроки", DataPath: "Объект.Строки",
		Children: []*metadata.FormElement{
			tpColumn("КолЦена", "Цена"),
			tpColumn("КолКоличество", "Количество"),
		},
	})
	cols := parseManagedTPColumns(t, tpColumnsFormHTML(t, s, order, id))

	if len(cols) != 3 {
		t.Fatalf("колонок %d, ожидалось 3 (две показываемые + скрытая): %+v", len(cols), cols)
	}
	if cols[0].ID != "Цена" || cols[1].ID != "Количество" {
		t.Errorf("порядок показа = %q, %q; ожидался порядок объявления колонок", cols[0].ID, cols[1].ID)
	}
	if cols[0].Hidden || cols[1].Hidden {
		t.Error("выбранная колонка помечена скрытой")
	}
	if cols[2].ID != "Сумма" || !cols[2].Hidden {
		t.Errorf("невыбранный реквизит = %+v, ожидался Сумма со скрытием", cols[2])
	}
	// Индекс колонки сервер сверяет с порядком МЕТАДАННЫХ (canonicalTPColumn).
	// Порядок показа с ним больше не совпадает, поэтому индекс едет отдельно.
	byName := map[string]int{}
	for _, col := range cols {
		byName[col.ID] = col.Index
	}
	for name, want := range map[string]int{"Количество": 0, "Цена": 1, "Сумма": 2} {
		if byName[name] != want {
			t.Errorf("колонка %q: index=%d, ожидался %d (позиция в метаданных ТЧ)", name, byName[name], want)
		}
	}
}

// Скрытая колонка остаётся В ФОРМЕ простой таблицы: спрятанный стилем input
// браузер отправляет как обычный, а вот выброшенная ячейка означала бы
// затирание реквизита (см. следующий тест).
func TestTPКолонки_СкрытаяКолонкаОстаётсяВPayloadПростойТаблицы(t *testing.T) {
	s, order, id := tpColumnsFixture(t, &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ТабСтроки", DataPath: "Объект.Строки",
		NoGrid: true,
		Children: []*metadata.FormElement{
			tpColumn("КолКоличество", "Количество"),
			tpColumn("КолЦена", "Цена"),
		},
	})
	rendered := tpColumnsFormHTML(t, s, order, id)

	if !strings.Contains(rendered, `name="tp.Строки.0.Сумма"`) {
		t.Error("ячейки скрытой колонки нет в форме — значение реквизита будет затёрто при записи")
	}
	if !strings.Contains(rendered, `data-tp-hidden-cols="[&#34;Сумма&#34;]"`) {
		t.Errorf("data-tp-hidden-cols не содержит скрытую колонку; перерисовка после события покажет её снова:\n%s",
			tpTableFragment(rendered))
	}
	// Ячейка спрятана стилем, а не удалена.
	if !strings.Contains(rendered, `style="display:none"`) {
		t.Error("скрытая колонка не спрятана стилем — она видна пользователю")
	}
}

// Характеризующий тест: он объясняет, ПОЧЕМУ скрытая колонка обязана оставаться
// в полезной нагрузке. Реквизит, которого нет в присланной строке, уходит в
// базу пустым — молча и во всех строках сразу.
func TestTPКолонки_ОтсутствиеКолонкиВPayloadЗатираетРеквизит(t *testing.T) {
	_, order, orderID := tpColumnsFixture(t, &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ТабСтроки", DataPath: "Объект.Строки",
	})
	form := order.Forms[0]

	body := strings.NewReader(`tp_json.Строки=[{"Количество":"2","Цена":"15"}]`)
	req := httptest.NewRequest(http.MethodPost, "/ui/document/Заказ/"+orderID.String(), body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatal(err)
	}
	rows, err := parseTablePartRowsForManagedForm(req, order, form, true)
	if err != nil {
		t.Fatalf("разбор payload: %v", err)
	}
	if len(rows["Строки"]) != 1 {
		t.Fatalf("строк %d, ожидалась 1", len(rows["Строки"]))
	}
	if got := rows["Строки"][0]["Сумма"]; got != nil {
		t.Fatalf("Сумма=%v; тест перестал описывать поведение записи — перечитайте, зачем скрытые колонки остаются в payload", got)
	}
}

// Событие на колонке доезжает до обработчика вместе с контекстом строки.
// Резолвится обычным путём элемента формы: клиент шлёт имя элемента-колонки,
// сервер сам решает, какая процедура ему соответствует.
func TestTPКолонки_СобытиеКолонкиВызываетсяСКонтекстом(t *testing.T) {
	column := tpColumn("КолЦена", "Цена")
	column.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnChange: "ЦенаПриИзменении"}
	srv, ent := setupManagedEventsServer(t, `
Процедура ЦенаПриИзменении()
	Сообщить(ИмяТабличнойЧасти);
	Сообщить(ТекущаяКолонка);
	Сообщить(НомерСтроки);
	Сообщить(ТекущаяСтрока.Цена);
КонецПроцедуры
`, nil, []*metadata.FormElement{{
		Kind: metadata.FormElementTablePart, Name: "ТабТовары", DataPath: "Объект.Товары",
		Children: []*metadata.FormElement{column},
	}})
	ent.TableParts = []metadata.TablePart{{
		Name: "Товары",
		Fields: []metadata.Field{
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Цена", Type: metadata.FieldTypeNumber},
		},
	}}

	body := url.Values{}
	body.Set("_element", "КолЦена")
	body.Set("_event", string(metadata.FormEventOnChange))
	body.Set("_kind", "object")
	body.Set("_tp", "Товары")
	body.Set("_tp_row", "1")
	body.Set("_tp_row_number", "2")
	body.Set("_tp_col", "Цена")
	body.Set("_tp_col_index", "1")
	body.Set("tp_json.Товары", `[{"Количество":1,"Цена":10},{"Количество":2,"Цена":20}]`)

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ожидался ok=true, error=%q", resp.Error)
	}
	want := []string{"Товары", "Цена", "2", "20"}
	if len(resp.Messages) != len(want) {
		t.Fatalf("messages=%v, ожидалось %v", resp.Messages, want)
	}
	for i := range want {
		if resp.Messages[i] != want[i] {
			t.Errorf("messages[%d]=%q, ожидалось %q (все messages=%v)", i, resp.Messages[i], want[i], resp.Messages)
		}
	}
}

// Колонка отправляет только правку своей ячейки. События строки принадлежат
// таблице целиком, и подделанный запрос не должен превращать колонку в их цель.
func TestTPКолонки_КолонкаНеОтправляетСобытийСтроки(t *testing.T) {
	column := tpColumn("КолЦена", "Цена")
	column.Handlers = map[metadata.FormEventType]string{
		metadata.FormEventOnChange:   "ЦенаПриИзменении",
		metadata.FormEventOnRowAdded: "ЦенаПриДобавленииСтроки",
	}
	srv, ent := setupManagedEventsServer(t, `
Процедура ЦенаПриИзменении()
	Сообщить("изменение");
КонецПроцедуры
Процедура ЦенаПриДобавленииСтроки()
	Сообщить("добавление");
КонецПроцедуры
`, nil, []*metadata.FormElement{{
		Kind: metadata.FormElementTablePart, Name: "ТабТовары", DataPath: "Объект.Товары",
		Children: []*metadata.FormElement{column},
	}})
	ent.TableParts = []metadata.TablePart{{Name: "Товары"}}

	body := url.Values{}
	body.Set("_element", "КолЦена")
	body.Set("_event", string(metadata.FormEventOnRowAdded))
	body.Set("_kind", "object")
	body.Set("_tp", "Товары")

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.OK {
		t.Fatalf("событие строки на колонке принято: messages=%v", resp.Messages)
	}
	if !strings.Contains(resp.Error, "не отправляет событие") {
		t.Errorf("error=%q, ожидался отказ «элемент не отправляет событие»", resp.Error)
	}
}

// Карта «реквизит → элемент-колонка» доезжает до клиента: по ней он решает,
// чей обработчик дёрнуть при правке ячейки.
func TestTPКолонки_КартаСобытийКолонокВРазметке(t *testing.T) {
	column := tpColumn("КолЦена", "Цена")
	column.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnChange: "ЦенаПриИзменении"}
	s, order, id := tpColumnsFixture(t, &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ТабСтроки", DataPath: "Объект.Строки",
		Children: []*metadata.FormElement{column, tpColumn("КолКоличество", "Количество")},
	})
	rendered := tpColumnsFormHTML(t, s, order, id)

	if !strings.Contains(rendered, `data-sg-colevents="{&#34;Цена&#34;:&#34;КолЦена&#34;}"`) {
		t.Errorf("карты событий колонок нет в разметке:\n%s", tpTableFragment(rendered))
	}
}

// Колонка без обработчика карту не порождает: лишний атрибут заставил бы
// клиента подписаться на onCellChange и гонять сеть у форм, которым это не
// нужно.
func TestTPКолонки_БезОбработчикаКартаНеРендерится(t *testing.T) {
	s, order, id := tpColumnsFixture(t, &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ТабСтроки", DataPath: "Объект.Строки",
		Children: []*metadata.FormElement{tpColumn("КолЦена", "Цена")},
	})
	if strings.Contains(tpColumnsFormHTML(t, s, order, id), "data-sg-colevents") {
		t.Error("data-sg-colevents отрисован без единого обработчика на колонке")
	}
}

// tpTableFragment вырезает кусок разметки вокруг грида — чтобы падение теста
// показывало разметку табличной части, а не всю страницу.
func tpTableFragment(rendered string) string {
	start := strings.Index(rendered, "data-sg-tp")
	if start < 0 {
		start = strings.Index(rendered, "data-tp-fields")
	}
	if start < 0 {
		return rendered
	}
	end := start + 900
	if end > len(rendered) {
		end = len(rendered)
	}
	return rendered[start:end]
}
