package ui

// Дата документа — реквизит «Дата», а не первый реквизит-дата.
//
// Документ, где СрокОплаты объявлен раньше Дата, писал движения на срок оплаты:
// период движений брался из первого реквизита-даты. Остатки на момент
// (МоментВремени берёт Дата) и отчёты по периодам расходились с движениями, а
// дата запрета проведения проверялась по тому же сроку — документ закрытого
// периода с поздним сроком оплаты проводился. Найдено ревью торговой
// конфигурации PuT: на этом упало заполнение демо-базы в PR её владельца.
//
// Проверяются все три пути проведения: форма и REST (entityservice.Save), DSL
// (Документы.X.ПолучитьОбъект().Провести()) и кнопка списка (POST …/post).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

var (
	dd23DocDate = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	dd23DueDate = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
)

func dd23Server(t *testing.T) (context.Context, *storage.DB, *Server, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "docdate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name:    "СчётНаОплату",
		Kind:    metadata.KindDocument,
		Posting: true,
		// Порядок намеренный: срок оплаты объявлен раньше даты документа.
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "СрокОплаты", Type: metadata.FieldTypeDate},
			{Name: "Дата", Type: metadata.FieldTypeDate},
		},
		TableParts: []metadata.TablePart{{Name: "Товары", Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		}}},
	}
	reg := &metadata.Register{
		Name:       "ОстаткиТоваров",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}
	onPost := `Процедура ОбработкаПроведения()
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Дв = Движения.ОстаткиТоваров.Добавить();
    Дв.ВидДвижения = "Приход";
    Дв.Номенклатура = Стр.Номенклатура;
    Дв.Количество = Стр.Количество;
  КонецЦикла;
КонецПроцедуры`
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Programs:  map[string]*ast.Program{doc.Name: mustParse(t, onPost)},
		Registers: []*metadata.Register{reg},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = &entityservice.Service{
		Store:        db,
		Reg:          registry,
		Interp:       interp,
		PrepareHook:  s.enrichHeaderRefs,
		EnrichTPRows: s.enrichTPRowsWithRefs,
		BuildVars:    s.buildDSLVarsWithMessagesTx,
		MakeThis: func(ctx context.Context, ctxSrc interpreter.CtxSource, obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return s.newFormObjectThisLive(ctx, ctxSrc, obj, e, nil, false)
		},
	}
	return ctx, db, s, doc
}

func dd23Fields(num string) map[string]any {
	return map[string]any{"Номер": num, "СрокОплаты": dd23DueDate, "Дата": dd23DocDate}
}

func dd23Rows() map[string][]map[string]any {
	return map[string][]map[string]any{"Товары": {{"Номенклатура": "Стол", "Количество": float64(3)}}}
}

// dd23Create записывает непроведённый документ.
func dd23Create(t *testing.T, ctx context.Context, s *Server, doc *metadata.Entity, num string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc, ID: id, IsNew: true, Fields: dd23Fields(num), TablePartRows: dd23Rows(),
	}); err != nil {
		t.Fatalf("запись документа %s: %v", num, err)
	}
	return id
}

// dd23Paths — три пути проведения; каждый возвращает текст отказа или "".
func dd23Paths(ctx context.Context, s *Server, doc *metadata.Entity) map[string]func(t *testing.T, id uuid.UUID, num string) string {
	return map[string]func(t *testing.T, id uuid.UUID, num string) string{
		"форма и REST": func(t *testing.T, id uuid.UUID, num string) string {
			res, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
				Entity: doc, ID: id, Action: "post", Fields: dd23Fields(num), TablePartRows: dd23Rows(),
			})
			if err != nil {
				return err.Error()
			}
			// Отказ хука или даты запрета приходит текстом, а не ошибкой.
			return res.DSLError
		},
		"DSL": func(t *testing.T, id uuid.UUID, _ string) (refusal string) {
			dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get(doc.Name).(*docProxy)
			loaded, err := dp.LoadObject(id.String())
			if err != nil {
				t.Fatalf("ПолучитьОбъект: %v", err)
			}
			defer func() {
				if r := recover(); r != nil {
					refusal = fmt.Sprint(r)
				}
			}()
			loaded.(*docWriter).CallMethod("провести", nil)
			return ""
		},
		"список": func(t *testing.T, id uuid.UUID, _ string) string {
			req := reqWithChi(http.MethodPost, "/ui/document/"+doc.Name+"/"+id.String()+"/post", nil,
				map[string]string{"entity": doc.Name, "id": id.String()})
			rec := httptest.NewRecorder()
			s.postDocument(rec, req)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("postDocument: код %d, тело %s", rec.Code, rec.Body.String())
			}
			if loc := rec.Header().Get("Location"); strings.Contains(loc, "posting_error=") {
				return loc
			}
			return ""
		},
	}
}

func dd23Periods(t *testing.T, ctx context.Context, db *storage.DB, id uuid.UUID) []time.Time {
	t.Helper()
	rows, err := db.Query(ctx, "SELECT period FROM рег_остаткитоваров WHERE recorder = ?", id.String())
	if err != nil {
		t.Fatalf("чтение движений: %v", err)
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		switch v := raw.(type) {
		case time.Time:
			out = append(out, v)
		case string:
			out = append(out, runtime.AsTime(v))
		default:
			t.Fatalf("period %T %v", raw, raw)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestПериодДвижений_ПоДатеДокумента_АНеПоПервомуРеквизитуДате(t *testing.T) {
	ctx, db, s, doc := dd23Server(t)
	for path, post := range dd23Paths(ctx, s, doc) {
		t.Run(path, func(t *testing.T) {
			id := dd23Create(t, ctx, s, doc, path)
			if refusal := post(t, id, path); refusal != "" {
				t.Fatalf("проведение отклонено: %s", refusal)
			}
			periods := dd23Periods(t, ctx, db, id)
			if len(periods) != 1 {
				t.Fatalf("движений %d, ожидалось 1", len(periods))
			}
			if !periods[0].Equal(dd23DocDate) {
				t.Errorf("период движения = %s, ожидалась дата документа %s (срок оплаты %s)",
					periods[0].UTC().Format(time.RFC3339), dd23DocDate.Format(time.RFC3339), dd23DueDate.Format(time.RFC3339))
			}
		})
	}
}

func TestДатаЗапрета_ПоДатеДокумента_АНеПоСрокуОплаты(t *testing.T) {
	ctx, db, s, doc := dd23Server(t)
	// Документ — в закрытом периоде, срок оплаты — после даты запрета.
	lock := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	if err := db.SavePostingLockDate(ctx, lock); err != nil {
		t.Fatal(err)
	}
	for path, post := range dd23Paths(ctx, s, doc) {
		t.Run(path, func(t *testing.T) {
			id := dd23Create(t, ctx, s, doc, path)
			refusal := post(t, id, path)
			if !strings.Contains(refusal, "запрещено") && !strings.Contains(refusal, "posting_error=") {
				t.Errorf("документ от %s провёлся при дате запрета %s: отказ %q",
					dd23DocDate.Format("02.01.2006"), lock.Format("02.01.2006"), refusal)
			}
			if periods := dd23Periods(t, ctx, db, id); len(periods) != 0 {
				t.Errorf("у отклонённого документа %d движений", len(periods))
			}
		})
	}
}
