package ui

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

// Виртуальная колонка ТЧ (#845) объявляется клиенту как строковая (Type:
// string в data-sg-cols), поэтому в ячейке показывается ровно то, что
// подставил сервер: форматировать по типу на клиенте нечем. Значит собирать
// строку обязан сервер — и одинаково на обоих диалектах.
//
// Без матричного теста расхождение тихое: драйвер SQLite отдаёт boolean как
// int64, дату как строку, а pgx — bool и time.Time. Раздельные тесты этого не
// показывают (юнит на SQLite зелёный со своим ожиданием), а пользователь видит
// «1» вместо «Да» на одной СУБД и «true» на другой.
func TestVirtualTPColumn_ТипизированныеЗначенияОдинаковыНаДиалектах(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		s, order, orderID := typedVirtualColumnFixture(t, db)
		rows := parseManagedTPRows(t, virtualColumnFormHTML(t, s, order, orderID))
		if len(rows) != 2 {
			t.Fatalf("строк ТЧ %d, ожидалось 2", len(rows))
		}

		for _, tc := range []struct {
			column string
			want   string
		}{
			{"КодКлиента", "К-000042"},
			{"АктивенКлиент", "true"},
			{"НеактивенКлиент", "false"},
			{"ДатаРождения", "1985-03-14"},
			{"РейтингКлиента", "4.5"},
		} {
			if got := rows[0][tc.column]; got != tc.want {
				t.Errorf("колонка %s = %#v, ожидалось %q", tc.column, got, tc.want)
			}
			// Строка без ссылки — пустая ячейка на обоих диалектах.
			if got := rows[1][tc.column]; got != "" {
				t.Errorf("колонка %s в строке без ссылки = %#v, ожидалась пустая", tc.column, got)
			}
		}
	})
}

func typedVirtualColumnFixture(t *testing.T, db *storage.DB) (*Server, *metadata.Entity, uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	client := &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},
			{Name: "Неактивен", Type: metadata.FieldTypeBool},
			{Name: "ДатаРождения", Type: metadata.FieldTypeDate},
			{Name: "Рейтинг", Type: metadata.FieldTypeNumber},
		},
	}
	order := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{
				{Name: "Клиент", Type: metadata.FieldType("reference:Клиент"), RefEntity: "Клиент"},
				{Name: "Сумма", Type: metadata.FieldTypeNumber},
			},
		}},
	}
	order.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: order.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки",
			VirtualColumns: []metadata.FormVirtualColumn{
				{Name: "КодКлиента", DataPath: "Клиент.Код"},
				{Name: "АктивенКлиент", DataPath: "Клиент.Активен"},
				{Name: "НеактивенКлиент", DataPath: "Клиент.Неактивен"},
				{Name: "ДатаРождения", DataPath: "Клиент.ДатаРождения"},
				{Name: "РейтингКлиента", DataPath: "Клиент.Рейтинг"},
			},
		}},
	}}

	if err := db.Migrate(ctx, []*metadata.Entity{client, order}); err != nil {
		t.Fatal(err)
	}
	clientID := uuid.New()
	if err := db.Upsert(ctx, client.Name, clientID, map[string]any{
		"Наименование": "ООО Ромашка",
		"Код":          "К-000042",
		"Активен":      true,
		"Неактивен":    false,
		"ДатаРождения": time.Date(1985, 3, 14, 0, 0, 0, 0, time.Local),
		"Рейтинг":      4.5,
	}, client); err != nil {
		t.Fatal(err)
	}
	orderID := uuid.New()
	if err := db.Upsert(ctx, order.Name, orderID,
		map[string]any{"Дата": time.Now()}, order); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTablePartRows(ctx, order.Name, "Строки", orderID, []map[string]any{
		{"Клиент": clientID.String(), "Сумма": 100},
		{"Клиент": nil, "Сумма": 200},
	}, order.TableParts[0]); err != nil {
		t.Fatal(err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{client, order}})
	s := &Server{
		store:       db,
		reg:         reg,
		messages:    NewMessageStore(),
		widgetCache: widget.NewCache(time.Minute),
	}
	return s, order, orderID
}
