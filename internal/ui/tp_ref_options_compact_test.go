package ui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

// Варианты ссылок табличной части на управляемой форме — один блок, у
// варианта только идентификатор и подпись. Раньше блок выводился дважды
// (свой и общий form-shared-js), а вариант уезжал записью справочника
// целиком: страница заявки колл-центра весила 2,8 МБ.
func TestManagedForm_TPRefOptionsCompactAndOnce(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "tpref.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	деталь := &metadata.Entity{Name: "Деталь", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Описание", Type: metadata.FieldTypeString},
	}}
	заказ := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
			{Name: "Деталь", Type: "reference:Деталь", RefEntity: "Деталь"},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		}}},
	}
	заказ.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: заказ.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "ТаблицаСтроки", DataPath: "Объект.Строки",
			Children: []*metadata.FormElement{tpColumn("КолДеталь", "Деталь"), tpColumn("КолКоличество", "Количество")},
		}},
	}}
	if err := db.Migrate(ctx, []*metadata.Entity{деталь, заказ}); err != nil {
		t.Fatal(err)
	}
	детальID := uuid.New()
	if err := db.Upsert(ctx, деталь.Name, детальID, map[string]any{"Наименование": "Вентилятор", "Описание": "ДЛИННОЕ-ОПИСАНИЕ-ДЕТАЛИ"}, деталь); err != nil {
		t.Fatal(err)
	}
	заказID := uuid.New()
	if err := db.Upsert(ctx, заказ.Name, заказID, map[string]any{"Дата": time.Now()}, заказ); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTablePartRows(ctx, заказ.Name, "Строки", заказID, []map[string]any{{"Деталь": детальID.String(), "Количество": 1}}, заказ.TableParts[0]); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{деталь, заказ}})
	s := &Server{store: db, reg: reg, messages: NewMessageStore(), widgetCache: widget.NewCache(time.Minute)}

	html := tpColumnsFormHTML(t, s, заказ, заказID)
	if strings.Contains(html, `id="ob-tp-ref-opts"`) {
		t.Fatal("на управляемой форме — второй блок вариантов ob-tp-ref-opts")
	}
	m := regexp.MustCompile(`(?s)<script type="application/json" id="ob-managed-tp-ref-opts">(.*?)</script>`).FindStringSubmatch(html)
	if m == nil {
		t.Fatal("нет блока ob-managed-tp-ref-opts")
	}
	var opts map[string]map[string][]map[string]any
	if err := json.Unmarshal([]byte(m[1]), &opts); err != nil {
		t.Fatalf("блок вариантов — не JSON: %v", err)
	}
	rows := opts["Строки"]["Деталь"]
	if len(rows) == 0 {
		t.Fatalf("нет вариантов колонки «Деталь»: %s", m[1])
	}
	for _, row := range rows {
		if len(row) != 2 || row["id"] == nil || row["_label"] == nil {
			t.Fatalf("вариант не сокращён до id и подписи: %v", row)
		}
	}
	if strings.Contains(m[1], "ДЛИННОЕ-ОПИСАНИЕ-ДЕТАЛИ") {
		t.Fatal("в варианты уехали поля записи справочника")
	}
}
