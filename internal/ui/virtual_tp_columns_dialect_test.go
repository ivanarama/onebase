package ui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

// Матричная проверка #1071: текст в ячейке ВИРТУАЛЬНОЙ колонки ТЧ (#845) должен
// зависеть только от типа реквизита цели, но не от диалекта СУБД.
//
// До фикса в ячейку клался `fmt.Sprintf("%v", v)` — внутреннее представление Go,
// и для bool с date оно расходилось: «1» против «true», а дата печаталась как
// «1985-03-13 21:00:00 +0000 UTC» против «1985-03-14 00:00:00 +0300 MSK», причём
// на SQLite днём РАНЬШЕ записанного.
//
// Раздельные тесты такое не ловят: у фичи их было девять, все на SQLite и все по
// строковому реквизиту цели, поэтому расхождение жило тихо.
//
// Путь строго публичный (правило про #611): запись цели — POST
// /ui/catalog/Клиент/new, та же форма, что заполняет человек; чтение — GET
// /ui/document/Заказ/{id} через смонтированный роутер. Ни fillVirtualColumn, ни
// applyVirtualTPColumns напрямую не зовутся.

// vc1071Cells — содержимое ячеек отрисованной managed-формы.
type vc1071Cells struct {
	virtual map[string]string // имя виртуальной колонки → текст ячейки
	stored  map[string]string // имя хранимого реквизита ТЧ → текст ячейки
	colType map[string]string // id колонки → type из data-sg-cols
}

func vc1071Entities() (client, order *metadata.Entity) {
	client = &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},  // истина
			{Name: "Архивный", Type: metadata.FieldTypeBool}, // ложь
			{Name: "ДатаРег", Type: metadata.FieldTypeDate},  // 14.03.1985
			{Name: "Скидка", Type: metadata.FieldTypeNumber}, // 4.5
		},
	}
	order = &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{
				{Name: "Клиент", Type: metadata.FieldType("reference:Клиент"), RefEntity: "Клиент"},
			},
		}},
	}
	order.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: order.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки",
			VirtualColumns: []metadata.FormVirtualColumn{
				{Name: "ВСтрока", DataPath: "Клиент.Код"},
				{Name: "ВБулИстина", DataPath: "Клиент.Активен"},
				{Name: "ВБулЛожь", DataPath: "Клиент.Архивный"},
				{Name: "ВДата", DataPath: "Клиент.ДатаРег"},
				{Name: "ВЧисло", DataPath: "Клиент.Скидка"},
			},
		}},
	}}
	return client, order
}

func vc1071Server(t *testing.T, db *storage.DB, entities []*metadata.Entity) *Server {
	t.Helper()
	ctx := context.Background()
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	s := &Server{
		store:       db,
		reg:         reg,
		interp:      interp,
		lockMgr:     runtime.NewLockManager(),
		messages:    NewMessageStore(),
		widgetCache: widget.NewCache(time.Minute),
		aiChatLimit: newAIWindowLimiter(1000, time.Minute),
	}
	s.entitySvc = &entityservice.Service{
		Store:        db,
		Reg:          reg,
		Interp:       interp,
		PrepareHook:  s.enrichHeaderRefs,
		EnrichTPRows: s.enrichTPRowsWithRefs,
		BuildVars:    s.buildDSLVarsWithMessagesTx,
		MakeThis: func(ctx context.Context, ctxSrc interpreter.CtxSource, obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return s.newFormObjectThisLive(ctx, ctxSrc, obj, e, nil, false)
		},
	}
	return s
}

// vc1071CreateClient заводит цель ссылки через ПУБЛИЧНУЮ форму создания элемента
// справочника. Значения передаются ровно в том виде, в каком их шлёт браузер
// (<input type="date"> → «1985-03-14»), поэтому вход на обоих диалектах
// побайтово одинаков — и всё расхождение, если оно есть, наживает движок.
func vc1071CreateClient(t *testing.T, ts *httptest.Server) uuid.UUID {
	t.Helper()
	form := url.Values{
		"Наименование": {"ООО Ромашка"},
		"Код":          {"К-000042"},
		"Активен":      {"true"},
		"Архивный":     {"false"},
		"ДатаРег":      {"1985-03-14"},
		"Скидка":       {"4.5"},
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(ts.URL+"/ui/catalog/"+url.PathEscape("Клиент")+"/new", form) //nolint:noctx // тестовый сервер
	if err != nil {
		t.Fatalf("POST создания клиента: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // тело читается ниже
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		t.Fatalf("создание клиента вернуло %d: %s", resp.StatusCode, vc1071Truncate(string(body)))
	}
	for _, part := range strings.Split(strings.Trim(resp.Header.Get("Location"), "/"), "/") {
		if id, err := uuid.Parse(part); err == nil {
			return id
		}
	}
	t.Fatalf("в редиректе после создания нет id: %q", resp.Header.Get("Location"))
	return uuid.Nil
}

func vc1071Truncate(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

// vc1071Measure проходит весь путь пользователя и возвращает содержимое ячеек.
func vc1071Measure(t *testing.T, db *storage.DB) vc1071Cells {
	t.Helper()
	ctx := context.Background()
	client, order := vc1071Entities()
	s := vc1071Server(t, db, []*metadata.Entity{client, order})

	r := chi.NewRouter()
	s.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	clientID := vc1071CreateClient(t, ts)

	orderID := uuid.New()
	if err := db.Upsert(ctx, order.Name, orderID,
		map[string]any{"Дата": time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)}, order); err != nil {
		t.Fatalf("Upsert заказа: %v", err)
	}
	if err := db.UpsertTablePartRows(ctx, order.Name, "Строки", orderID, []map[string]any{{
		"Клиент": clientID.String(),
	}}, order.TableParts[0]); err != nil {
		t.Fatalf("UpsertTablePartRows: %v", err)
	}

	htmlOut := getBody(t, ts.URL+"/ui/document/"+url.PathEscape(order.Name)+"/"+orderID.String())

	rows := parseManagedTPRows(t, htmlOut)
	if len(rows) != 1 {
		t.Fatalf("строк ТЧ %d, ожидалась 1", len(rows))
	}
	out := vc1071Cells{
		virtual: map[string]string{},
		stored:  map[string]string{},
		colType: map[string]string{},
	}
	for _, c := range parseManagedTPColumns(t, htmlOut) {
		out.colType[c.ID] = c.Type
	}
	for _, name := range []string{"ВСтрока", "ВБулИстина", "ВБулЛожь", "ВДата", "ВЧисло"} {
		out.virtual[name] = fmt.Sprintf("%v", rows[0][name])
	}
	return out
}

// vc1071ForceOffsetZone уводит процесс из UTC на время теста.
//
// Без этого проверка ДАТЫ теряет половину смысла: съезд календарного дня
// возникает только на хосте со смещением от UTC, и на UTC-раннере CI тест
// остался бы зелёным на сломанном коде. Зона фиксированная, а не
// LoadLocation("Europe/Moscow"), потому что база tzdata есть не в каждом
// контейнере, и тест не должен зависеть от её наличия.
func vc1071ForceOffsetZone(t *testing.T) {
	t.Helper()
	saved := time.Local
	time.Local = time.FixedZone("MSK", 3*60*60)
	t.Cleanup(func() { time.Local = saved })
}

func TestVirtualTPColumn_ТипЦелиРешаетТекстЯчейки_НеДиалект(t *testing.T) {
	vc1071ForceOffsetZone(t)

	// Ожидания одинаковы для обоих диалектов — в этом и смысл проверки.
	want := map[string]string{
		"ВСтрока":    "К-000042",
		"ВЧисло":     "4.5",
		"ВБулИстина": "✓", // как рисует хранимую bool-колонку клиентский форматтер
		"ВБулЛожь":   "—", // не пусто: пустая ячейка уже означает «ссылки нет»
		"ВДата":      "14.03.1985",
	}
	names := []string{"ВСтрока", "ВЧисло", "ВБулИстина", "ВБулЛожь", "ВДата"}

	measured := map[string]vc1071Cells{}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dialect := "sqlite"
		if strings.Contains(t.Name(), "postgres") {
			dialect = "postgres"
		}
		cells := vc1071Measure(t, db)
		measured[dialect] = cells

		for _, n := range names {
			if got := cells.virtual[n]; got != want[n] {
				t.Errorf("[%s] виртуальная колонка %s: получено %q, ожидалось %q",
					dialect, n, got, want[n])
			}
		}
		// Календарный день — отдельно от формата: именно он съезжал на SQLite,
		// и именно его не поймать сравнением диалектов между собой.
		if day := cells.virtual["ВДата"]; !strings.HasPrefix(day, "14.03.1985") {
			t.Errorf("[%s] в ячейке даты день %q, а записано 14.03.1985", dialect, day)
		}
		// Клиенту колонка объявлена строковой, и типового рендера у неё нет —
		// поэтому текст обязан быть готовым уже на сервере.
		if got := cells.colType["ВДата"]; got != string(metadata.FieldTypeString) {
			t.Errorf("[%s] тип виртуальной колонки на клиенте %q, ожидался %q",
				dialect, got, metadata.FieldTypeString)
		}
	})

	sq, okS := measured["sqlite"]
	pg, okP := measured["postgres"]
	if !okS || !okP {
		t.Log("сравнение диалектов пропущено: postgres не прогонялся (нет TEST_DATABASE_URL)")
		return
	}
	for _, n := range names {
		if sq.virtual[n] != pg.virtual[n] {
			t.Errorf("виртуальная колонка %s расходится по диалектам: sqlite=%q postgres=%q",
				n, sq.virtual[n], pg.virtual[n])
		}
	}
}
