package exchange_test

// Обмен и страховка значений перечислений (#962, Н3 + #1037).
//
// Страховка на уровне записи отклоняет неизвестное значение перечисления — но
// не для обмена: узел-приёмник может работать на другой версии конфигурации,
// где значения отличаются, и обрывать репликацию из-за одного реквизита дороже,
// чем принять запись. Что делать с такими значениями по существу — отдельное
// решение (#1037).
//
// Тест закрепляет именно исключение. Без него обмен рвался бы молча и не сразу:
// пакеты ходят, пока конфигурации совпадают, а расхождение версий всплывает
// у заказчика. Особое внимание строкам табличной части: они пишутся отдельным
// вызовом, и «пометили шапку, забыли строки» — самая правдоподобная поломка.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
)

type bypassEnums struct{ enums []*metadata.Enum }

func (b bypassEnums) Enums() []*metadata.Enum { return b.enums }
func (b bypassEnums) GetEnum(name string) *metadata.Enum {
	for _, e := range b.enums {
		if e.Name == name {
			return e
		}
	}
	return nil
}

func TestApplyPackage_ForeignEnumValuePassesBackstop(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Статус", Type: metadata.FieldTypeString, EnumName: "СтатусЗаявки"},
		},
		TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
			{Name: "Товар", Type: metadata.FieldTypeString},
			{Name: "СостояниеСтроки", Type: metadata.FieldTypeString, EnumName: "СтатусЗаявки"},
		}}},
	}
	db, ctx := newBase(t, ent)
	if err := db.SaveExchangeThisNode(ctx, "Обмен", "fil01"); err != nil {
		t.Fatal(err)
	}
	// Локальная конфигурация знает два значения; узел-отправитель прислал третье.
	db.SetEnumSource(bypassEnums{enums: []*metadata.Enum{
		{Name: "СтатусЗаявки", Values: []string{"Новая", "Закрыта"}},
	}})

	id := uuid.New()
	pkg := exchange.Package{
		Format: exchange.FormatV1, Plan: "Обмен", FromNode: "CENTER", ToNode: "FIL01", MessageNo: 1,
		Objects: []exchange.PackageObject{{
			Type: "заявка", ID: strings.ToUpper(id.String()), Version: 2, ChangedAt: 2000,
			Fields: map[string]any{"Наименование": "изЦентра", "Статус": "ВРаботе"},
			TableParts: map[string][]map[string]any{
				"Строки": {{"Товар": "Стол", "СостояниеСтроки": "ВРаботе"}},
			},
		}},
	}
	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}

	plan := &metadata.ExchangePlan{
		Name:    "Обмен",
		Content: []string{"Документ.Заявка"},
		Nodes:   []metadata.ExchangeNode{{Code: "center"}, {Code: "fil01"}},
	}
	plan.Normalize()
	if _, err := exchange.ApplyPackage(ctx, db, fakeResolver{"Заявка": ent}, plan, data, exchange.ApplyOptions{}); err != nil {
		t.Fatalf("обмен отклонён страховкой перечислений: %v", err)
	}

	row, err := db.GetByID(context.Background(), ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	if row == nil {
		t.Fatal("объект обмена не записан")
	}
	rows, err := db.GetTablePartRows(context.Background(), ent.Name, "Строки", id, ent.TableParts[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("строк табличной части %d, ожидалась 1 — строки обмена не доехали", len(rows))
	}
}
