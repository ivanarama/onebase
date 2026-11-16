package ui

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
	sqlite "modernc.org/sqlite"
)

// Открываем настоящую форму через HTTP. Счётчик чтений подписей в SQLite view
// обнаруживает и лишний List, результат которого впоследствии выбросил шаблон.
// Production-код, SQL и интерфейс storage для наблюдения не меняются.
func TestReferencePreloadOnFormOpenHTTP(t *testing.T) {
	for _, tc := range []struct {
		name                                      string
		managed, present, dropdown, scoped, empty bool
		wantOptions                               int
	}{
		{name: "dropdown", managed: true, present: true, dropdown: true, wantOptions: 8},
		{name: "collapsed", managed: true, present: true, wantOptions: 1},
		{name: "collapsed_empty", managed: true, present: true, empty: true},
		{name: "outside_form", managed: true, dropdown: true},
		{name: "scoped", managed: true, present: true, dropdown: true, scoped: true, wantOptions: 2},
		{name: "automatic", present: true, dropdown: true, wantOptions: 8},
		{name: "automatic_empty", present: true, dropdown: true, empty: true, wantOptions: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var reads atomic.Int64
			// Отдельная функция на fixture: не разделяет счётчик с другими тестами
			// и остаётся безопасной при повторном запуске теста в том же процессе.
			probe := "ref_preload_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			sqlite.MustRegisterScalarFunction(probe, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				reads.Add(1)
				return args[0], nil
			})
			db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "preload.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			target := &metadata.Entity{Name: "Товары", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Доступен", Type: metadata.FieldTypeBool},
			}}
			doc := &metadata.Entity{Name: "Заказ", Kind: metadata.KindDocument, Fields: []metadata.Field{
				{Name: "Товар", Type: metadata.FieldType("reference:Товары"), RefEntity: "Товары"},
			}}
			if tc.managed {
				form := &metadata.FormModule{Name: "ФормаОбъекта", EntityName: doc.Name, Kind: "object", LayoutKind: metadata.FormLayoutManaged}
				if tc.present {
					dropdown := tc.dropdown
					field := &metadata.FormElement{ID: "product", Name: "ПолеТовар", Kind: metadata.FormElementField, DataPath: "Объект.Товар", ChoiceDropdown: &dropdown}
					if tc.scoped {
						yes := true
						field.ChoiceFilter = []metadata.FormChoiceCondition{{Field: "Доступен", Op: metadata.FormChoiceOpEqual, Value: &yes}}
					}
					form.Elements = []*metadata.FormElement{{Kind: metadata.FormElementGroupBox, Name: "Группа", Children: []*metadata.FormElement{field}}}
				}
				doc.Forms = []*metadata.FormModule{form}
			}
			entities := []*metadata.Entity{target, doc}
			if err := db.Migrate(ctx, entities); err != nil {
				t.Fatal(err)
			}
			selected := uuid.MustParse("00000000-0000-0000-0000-000000000001")
			for i := 1; i <= 8; i++ {
				id := uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", i))
				if err := db.Upsert(ctx, target.Name, id, map[string]any{"Наименование": fmt.Sprintf("Товар %d", i), "Доступен": i <= 2}, target); err != nil {
					t.Fatal(err)
				}
			}
			ownerID := uuid.New()
			values := map[string]any{}
			if !tc.empty {
				values["Товар"] = selected.String()
			}
			if err := db.Upsert(ctx, doc.Name, ownerID, values, doc); err != nil {
				t.Fatal(err)
			}

			// View сохраняет схему и данные и добавляет наблюдение только при
			// фактическом чтении Наименование. Поиск по id читает одну подпись,
			// общая страница возвращает восемь строк, scoped-страница — две.
			// Число вычислений view зависит от SQL-плана, а не только от числа
			// строк: один List может исполнять несколько SELECT и COUNT.
			columns, err := db.QueryAll(ctx, `PRAGMA table_info("товары")`)
			if err != nil {
				t.Fatal(err)
			}
			quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
			var projection []string
			for _, column := range columns {
				name := fmt.Sprint(column["name"])
				value := quote(name)
				if strings.EqualFold(name, "Наименование") {
					value = probe + "(" + value + ") AS " + quote(name)
				}
				projection = append(projection, value)
			}
			if _, err := db.Exec(ctx, `ALTER TABLE "товары" RENAME TO "preload_rows"`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `CREATE VIEW "товары" AS SELECT `+strings.Join(projection, ",")+` FROM "preload_rows"`); err != nil {
				t.Fatal(err)
			}
			// Калибруем ровно один разрешённый публичный List на той же fixture.
			// У цели нет owner/activity/RLS; UI исключает группы, ограничивает
			// страницу и для scoped добавляет только Доступен = true.
			params := storage.ListParams{Limit: refPickerDefaultLimit, ExcludeFolders: true}
			if tc.scoped {
				params.ChoicePredicates = []storage.ChoicePredicate{{Field: "Доступен", Op: metadata.FormChoiceOpEqual, Value: true}}
			}
			maxReads := int64(0)
			if tc.present && tc.dropdown {
				reads.Store(0)
				rows, err := db.List(ctx, target.Name, target, params)
				if err != nil {
					t.Fatal(err)
				}
				maxReads = reads.Load()
				if len(rows) != tc.wantOptions || maxReads == 0 {
					t.Fatalf("List calibration: rows=%d, reads=%d", len(rows), maxReads)
				}
			}
			// Collapsed/outside_form читают только выбранную подпись по id.
			// Scoped сначала сохраняет эту подпись, затем получает scoped-список.
			// Обычный dropdown уже содержит выбранную строку, добавлять её не надо.
			if !tc.empty && (!tc.present || !tc.dropdown || tc.scoped) {
				maxReads++
			}
			reg := runtime.NewRegistry()
			reg.Load(runtime.LoadOptions{Entities: entities})
			server := &Server{reg: reg, store: db}
			server.entitySvc = server.newEntityService(nil)
			router := chi.NewRouter()
			server.Mount(router)
			request := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(doc.Name)+"/"+ownerID.String(), nil)
			recorder := httptest.NewRecorder()
			reads.Store(0)
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("form status=%d: %s", recorder.Code, recorder.Body.String())
			}
			gotReads := reads.Load()
			if gotReads > maxReads {
				t.Errorf("read %d reference labels, want at most %d (unnecessary preload)", gotReads, maxReads)
			}
			if maxReads > 0 && gotReads == 0 {
				t.Error("probe did not observe reference reads")
			}
			t.Logf("HTTP label computations=%d, budget=%d", gotReads, maxReads)
			// Отрицательный контроль: настоящий дополнительный List после GET
			// обязан нарушить ту же границу, даже если UI выбросил его результат.
			// Scoped отдельно проверяет и повтор scoped, и лишний общий список.
			controls := []storage.ListParams{params}
			if tc.scoped {
				controls = append(controls, storage.ListParams{Limit: refPickerDefaultLimit, ExcludeFolders: true})
			}
			for i, extra := range controls {
				reads.Store(gotReads)
				if _, err := db.List(ctx, target.Name, target, extra); err != nil {
					t.Fatal(err)
				}
				if withExtra := reads.Load(); withExtra <= maxReads {
					t.Errorf("negative control %d: extra List reads=%d did not exceed budget=%d", i, withExtra, maxReads)
				} else {
					t.Logf("negative control %d: extra List reads=%d exceeds budget=%d", i, withExtra, maxReads)
				}
			}
			document, err := html.Parse(strings.NewReader(recorder.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			selectNode := findSelectByName(document, "Товар")
			if !tc.present {
				if selectNode != nil {
					t.Fatal("field outside managed form was rendered")
				}
				return
			}
			if selectNode == nil {
				t.Fatal("reference select missing")
			}
			options := map[string]string{}
			selectedCount := 0
			for option := selectNode.FirstChild; option != nil; option = option.NextSibling {
				if option.Type != html.ElementNode || option.Data != "option" {
					continue
				}
				id, _ := htmlAttribute(option, "value")
				if id == "" {
					continue
				}
				label := ""
				if option.FirstChild != nil {
					label = option.FirstChild.Data
				}
				options[id] = label
				if _, ok := htmlAttribute(option, "selected"); ok {
					selectedCount++
					if id != selected.String() {
						t.Errorf("unexpected selected option %s", id)
					}
				}
			}
			if len(options) != tc.wantOptions {
				t.Fatalf("options=%v, want %d", options, tc.wantOptions)
			}
			for i := 1; i <= tc.wantOptions; i++ {
				id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
				if options[id] != fmt.Sprintf("Товар %d", i) {
					t.Errorf("option %s label=%q", id, options[id])
				}
			}
			if !tc.empty && (options[selected.String()] != "Товар 1" || selectedCount != 1) {
				t.Fatalf("selected label/value lost: %v, selected=%d", options, selectedCount)
			}
			if tc.empty && selectedCount != 0 {
				t.Fatal("empty reference gained a selection")
			}
			if tc.scoped {
				for i := 3; i <= 8; i++ {
					id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
					if _, ok := options[id]; ok {
						t.Fatalf("scoped dropdown leaked %s", id)
					}
				}
			}
		})
	}
}
