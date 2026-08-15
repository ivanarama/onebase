package exchange

// Приём пакета обмена терпим к НЕДОСТАЮЩИМ полям и строг к неизвестным
// (план 117D).
//
// Раньше требовалось точное совпадение набора: появление нового реквизита —
// например «Кода» при включении нумератора — роняло приём целиком, вместе с
// пакетами, уже лежащими в очереди и снятыми до изменения. Обновить все узлы
// одномоментно на живом обмене нереально.

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func shapeEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Контрагенты",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
}

func TestValidateObjectShape_MissingFieldAccepted(t *testing.T) {
	obj := PackageObject{ID: "1", Fields: map[string]any{"Наименование": "Альфа"}}
	if err := validateObjectShape(shapeEntity(), obj); err != nil {
		t.Fatalf("пакет без нового реквизита отклонён: %v", err)
	}
}

func TestValidateObjectShape_UnknownFieldRejected(t *testing.T) {
	obj := PackageObject{ID: "1", Fields: map[string]any{
		"Код": "К-1", "Наименование": "Альфа", "Чужое": "x",
	}}
	err := validateObjectShape(shapeEntity(), obj)
	if err == nil || !strings.Contains(err.Error(), "неизвестное поле") {
		t.Fatalf("неизвестное поле принято: %v", err)
	}
}

func TestValidateObjectShape_FullShapeStillValid(t *testing.T) {
	obj := PackageObject{ID: "1", Fields: map[string]any{"Код": "К-1", "Наименование": "Альфа"}}
	if err := validateObjectShape(shapeEntity(), obj); err != nil {
		t.Fatalf("полный набор отклонён: %v", err)
	}
}

// Те же правила — для строк табличных частей (#885).
//
// У шапки терпимость к недостающему полю появилась в 117D, а у ТЧ осталась
// проверка «набор совпадает ТОЧНО». Она ломала рекомендованный порядок
// обновления узлов: получателя обновляют первым, у него появляется новое поле
// ТЧ, отправитель его ещё не шлёт — и пакет отклонялся целиком, хотя
// недостающее поле означает ровно «незаполнено».
func shapeEntityWithTP() *metadata.Entity {
	e := shapeEntity()
	e.TableParts = []metadata.TablePart{{
		Name: "Товары",
		Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Скидка", Type: metadata.FieldTypeNumber}, // «новое» поле получателя
		},
	}}
	return e
}

func TestValidateObjectShape_MissingTablePartFieldAccepted(t *testing.T) {
	obj := PackageObject{
		ID:     "1",
		Fields: map[string]any{"Код": "К-1", "Наименование": "Альфа"},
		TableParts: map[string][]map[string]any{
			// Отправитель ещё не знает про «Скидку».
			"Товары": {{"Номенклатура": "Стул", "Количество": 2}},
		},
	}
	if err := validateObjectShape(shapeEntityWithTP(), obj); err != nil {
		t.Fatalf("строка ТЧ без нового поля отклонена: %v", err)
	}
}

// Неизвестное поле строки по-прежнему отклоняется — ровно как в шапке: это
// защита от чужого пакета и от опечатки, за которой стоит потерянное значение.
func TestValidateObjectShape_UnknownTablePartFieldRejected(t *testing.T) {
	obj := PackageObject{
		ID:     "1",
		Fields: map[string]any{"Код": "К-1", "Наименование": "Альфа"},
		TableParts: map[string][]map[string]any{
			"Товары": {{"Номенклатура": "Стул", "Количество": 2, "Чужое": "x"}},
		},
	}
	err := validateObjectShape(shapeEntityWithTP(), obj)
	if err == nil || !strings.Contains(err.Error(), "неизвестное поле") {
		t.Fatalf("неизвестное поле строки ТЧ принято: %v", err)
	}
}

// Пустая строка ТЧ — тоже законный крайний случай недостающих полей.
func TestValidateObjectShape_EmptyTablePartRowAccepted(t *testing.T) {
	obj := PackageObject{
		ID:         "1",
		Fields:     map[string]any{"Код": "К-1", "Наименование": "Альфа"},
		TableParts: map[string][]map[string]any{"Товары": {{}}},
	}
	if err := validateObjectShape(shapeEntityWithTP(), obj); err != nil {
		t.Fatalf("пустая строка ТЧ отклонена: %v", err)
	}
}
