package ui

import (
	"context"
	"fmt"
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
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

// Матричная проверка #1078: виртуальная колонка ТЧ показывает реквизит цели по
// его ТИПУ для ВСЕХ типов, а не только для тех двух, по которым расходились
// диалекты (#1071).
//
// До правки перечисление показывало код вместо подписи, ссылка — голый UUID,
// richtext — разметку с тегами. Корень тот же: тип реквизита известен, но при
// превращении в текст не использовался.
//
// Путь публичный: запись цели через storage, чтение — GET формы документа с
// разбором data-sg-rows отрисованной страницы.

func vct1078Entities() (city, client, order *metadata.Entity, enum *metadata.Enum) {
	enum = &metadata.Enum{
		Name:   "Приоритет",
		Values: []string{"Высокий", "Низкий"},
		ValueTitles: map[string]map[string]string{
			"Высокий": {"ru": "Высокий приоритет", "en": "High"},
		},
	}
	// Второй уровень разыменования: реквизит цели сам ссылочный.
	city = &metadata.Entity{
		Name: "Город", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	client = &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Приоритет", Type: metadata.FieldType("enum:Приоритет"), EnumName: "Приоритет"},
			{Name: "Город", Type: metadata.FieldType("reference:Город"), RefEntity: "Город"},
			{Name: "Заметка", Type: metadata.FieldTypeRichText},
		},
	}
	order = &metadata.Entity{
		Name: "Заказ1078", Kind: metadata.KindDocument,
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
				{Name: "ВПеречисление", DataPath: "Клиент.Приоритет"},
				{Name: "ВСсылка", DataPath: "Клиент.Город"},
				{Name: "ВРазметка", DataPath: "Клиент.Заметка"},
			},
		}},
	}}
	return city, client, order, enum
}

func vct1078ServerFor(t *testing.T, db *storage.DB, ents []*metadata.Entity, enums []*metadata.Enum) *Server {
	t.Helper()
	if err := db.Migrate(context.Background(), ents); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: ents, Enums: enums})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	s := &Server{
		store: db, reg: reg, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore(),
		widgetCache: widget.NewCache(time.Minute),
		aiChatLimit: newAIWindowLimiter(1000, time.Minute),
	}
	s.entitySvc = &entityservice.Service{
		Store: db, Reg: reg, Interp: interp,
		PrepareHook: s.enrichHeaderRefs, EnrichTPRows: s.enrichTPRowsWithRefs,
		BuildVars: s.buildDSLVarsWithMessagesTx,
		MakeThis: func(ctx context.Context, cs interpreter.CtxSource, o *runtime.Object, e *metadata.Entity) interpreter.This {
			return s.newFormObjectThisLive(ctx, cs, o, e, nil, false)
		},
	}
	return s
}

func vct1078Server(t *testing.T, db *storage.DB, ents []*metadata.Entity, enums []*metadata.Enum) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	vct1078ServerFor(t, db, ents, enums).Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func TestVirtualTPColumn_ВсеТипыРеквизитаЦели_1078(t *testing.T) {
	want := map[string]string{
		// Подпись значения, а не его код.
		"ВПеречисление": "Высокий приоритет",
		// Представление объекта, а не UUID.
		"ВСсылка": "Москва",
		// Текст без разметки.
		"ВРазметка": "жирно и курсивом",
	}

	measured := map[string]map[string]string{}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dialect := "sqlite"
		if strings.Contains(t.Name(), "postgres") {
			dialect = "postgres"
		}
		ctx := context.Background()
		city, client, order, enum := vct1078Entities()
		ts := vct1078Server(t, db, []*metadata.Entity{city, client, order}, []*metadata.Enum{enum})

		cityID := uuid.New()
		if err := db.Upsert(ctx, city.Name, cityID,
			map[string]any{"Наименование": "Москва"}, city); err != nil {
			t.Fatalf("Upsert города: %v", err)
		}
		clientID := uuid.New()
		if err := db.Upsert(ctx, client.Name, clientID, map[string]any{
			"Наименование": "ООО Ромашка",
			"Приоритет":    "Высокий",
			"Город":        cityID.String(),
			"Заметка":      "<p><b>жирно</b> и <i>курсивом</i></p>",
		}, client); err != nil {
			t.Fatalf("Upsert клиента: %v", err)
		}
		orderID := uuid.New()
		if err := db.Upsert(ctx, order.Name, orderID,
			map[string]any{"Дата": time.Now()}, order); err != nil {
			t.Fatalf("Upsert заказа: %v", err)
		}
		if err := db.UpsertTablePartRows(ctx, order.Name, "Строки", orderID, []map[string]any{{
			"Клиент": clientID.String(),
		}}, order.TableParts[0]); err != nil {
			t.Fatalf("UpsertTablePartRows: %v", err)
		}

		rows := parseManagedTPRows(t, getBody(t,
			ts.URL+"/ui/document/"+url.PathEscape(order.Name)+"/"+orderID.String()))
		if len(rows) != 1 {
			t.Fatalf("[%s] строк ТЧ %d, ожидалась 1", dialect, len(rows))
		}
		got := map[string]string{}
		for name := range want {
			got[name] = fmt.Sprintf("%v", rows[0][name])
		}
		measured[dialect] = got

		for name, expected := range want {
			if got[name] != expected {
				t.Errorf("[%s] виртуальная колонка %s: получено %q, ожидалось %q",
					dialect, name, got[name], expected)
			}
		}
		// Внутренние представления не должны доезжать до страницы.
		if strings.Contains(got["ВРазметка"], "<") {
			t.Errorf("[%s] в колонке richtext видна разметка: %q", dialect, got["ВРазметка"])
		}
		if _, _, isUUID := uuidFromValue(got["ВСсылка"]); isUUID {
			t.Errorf("[%s] в колонке-ссылке виден UUID вместо представления: %q",
				dialect, got["ВСсылка"])
		}
	})

	sq, okS := measured["sqlite"]
	pg, okP := measured["postgres"]
	if !okS || !okP {
		t.Log("сравнение диалектов пропущено: postgres не прогонялся (нет TEST_DATABASE_URL)")
		return
	}
	for name := range want {
		if sq[name] != pg[name] {
			t.Errorf("колонка %s расходится по диалектам: sqlite=%q postgres=%q",
				name, sq[name], pg[name])
		}
	}
}

// Второй уровень разыменования не обходит доступ.
//
// Первый уровень читается через readableFieldsByIDs, и было бы легко потерять
// эту строгость на втором: подпись ссылки в обычной строке ТЧ читается более
// свободным путём, и соблазн переиспользовать его здесь прямой. Обоснование
// строгости — в #845: колонка показывает ПРОИЗВОЛЬНЫЙ реквизит произвольной
// сущности, и исторический зазор подписи на неё не распространяется.
func TestVirtualTPColumn_ВторойУровеньНеОбходитДоступ_1078(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "vct.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.EnsureServiceSchema(ctx); err != nil {
		t.Fatalf("служебные таблицы: %v", err)
	}

	city, client, order, enum := vct1078Entities()
	srv := vct1078ServerFor(t, db, []*metadata.Entity{city, client, order}, []*metadata.Enum{enum})

	cityID := uuid.New()
	if err := db.Upsert(ctx, city.Name, cityID,
		map[string]any{"Наименование": "Москва"}, city); err != nil {
		t.Fatalf("Upsert города: %v", err)
	}
	clientID := uuid.New()
	if err := db.Upsert(ctx, client.Name, clientID, map[string]any{
		"Наименование": "ООО Ромашка",
		"Город":        cityID.String(),
	}, client); err != nil {
		t.Fatalf("Upsert клиента: %v", err)
	}
	orderID := uuid.New()
	if err := db.Upsert(ctx, order.Name, orderID,
		map[string]any{"Дата": time.Now()}, order); err != nil {
		t.Fatalf("Upsert заказа: %v", err)
	}
	if err := db.UpsertTablePartRows(ctx, order.Name, "Строки", orderID, []map[string]any{{
		"Клиент": clientID.String(),
	}}, order.TableParts[0]); err != nil {
		t.Fatalf("UpsertTablePartRows: %v", err)
	}

	// Клиент читается, а ГОРОД закрыт строковой политикой: доступен только
	// «Питер», а у клиента «Москва».
	restricted := &auth.User{Login: "u", Roles: []*auth.Role{{
		Permissions: auth.Permission{
			Documents: map[string][]string{order.Name: {"read", "write"}},
			Catalogs:  map[string][]string{"Клиент": {"read"}, "Город": {"read"}},
			RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
				"Город": {"read": {Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "Питер"}}},
			}},
		},
	}}}
	req := httptest.NewRequest(http.MethodGet, "/ui/document/"+order.Name+"/"+orderID.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "document")
	rctx.URLParams.Add("entity", order.Name)
	rctx.URLParams.Add("id", orderID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(auth.ContextWithUser(req.Context(), restricted))
	rec := httptest.NewRecorder()

	srv.formEdit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("карточка вернула %d: %s", rec.Code, rec.Body.String())
	}
	rows := parseManagedTPRows(t, rec.Body.String())
	if len(rows) == 0 {
		t.Fatal("строки ТЧ не отрисованы")
	}
	got := fmt.Sprintf("%v", rows[0]["ВСсылка"])
	if got == "Москва" {
		t.Error("представление закрытой политикой записи показано в виртуальной колонке")
	}
	if _, _, isUUID := uuidFromValue(got); isUUID {
		t.Errorf("вместо представления показан UUID: %q", got)
	}
}
