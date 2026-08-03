package runtime

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// *Object должен реализовывать тот же контракт, что и *Ref —
// GetRefUUID() и String(). Это позволяет storage.normalizeRegArg
// одинаково обрабатывать оба типа при записи движений регистра.
func TestObject_GetRefUUID(t *testing.T) {
	id := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	o := &Object{Type: "Контрагенты", Kind: metadata.KindCatalog, ID: id}
	if got := o.GetRefUUID(); got != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("GetRefUUID = %q", got)
	}
}

func TestObject_GetRefUUID_Nil(t *testing.T) {
	var o *Object
	if got := o.GetRefUUID(); got != "" {
		t.Errorf("nil-Object.GetRefUUID = %q, ожидался пустой", got)
	}
}

// String() — display-имя по конвенции Наименование/Name/Номер/Number.
func TestObject_String_Naimenovanie(t *testing.T) {
	o := &Object{
		Type:   "Контрагенты",
		Kind:   metadata.KindCatalog,
		ID:     uuid.New(),
		Fields: map[string]any{"наименование": "ООО Ромашка"},
	}
	if got := o.String(); got != "ООО Ромашка" {
		t.Errorf("String() = %q, ожидалось «ООО Ромашка»", got)
	}
}

func TestObject_String_Number(t *testing.T) {
	o := &Object{
		Type:   "ПоступлениеТоваров",
		Kind:   metadata.KindDocument,
		ID:     uuid.New(),
		Fields: map[string]any{"номер": "ПОС-00042"},
	}
	if got := o.String(); got != "ПОС-00042" {
		t.Errorf("String() = %q", got)
	}
}

// Существующий объект формы получает Fields с именами из метаданных
// (PascalCase), тогда как новый объект после Set хранит lowercase.
func TestObject_String_PascalCaseNumber(t *testing.T) {
	o := &Object{
		Type:   "ПриходныйКассовыйОрдер",
		Kind:   metadata.KindDocument,
		ID:     uuid.New(),
		Fields: map[string]any{"Номер": "ПКО-00001"},
	}
	if got := o.String(); got != "ПКО-00001" {
		t.Errorf("String() = %q, ожидалось значение PascalCase-поля Номер", got)
	}
}

// Без конвенционных полей — fallback на Type:short-uuid.
func TestObject_String_Fallback(t *testing.T) {
	o := &Object{
		Type:   "Документ",
		Kind:   metadata.KindDocument,
		ID:     uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		Fields: map[string]any{},
	}
	got := o.String()
	if got != "Документ:44444444" {
		t.Errorf("String() fallback = %q", got)
	}
}

func TestObject_String_Nil(t *testing.T) {
	var o *Object
	if got := o.String(); got != "" {
		t.Errorf("nil-Object.String() = %q", got)
	}
}

// ЭтотОбъект.МоментВремени() возвращает MomentTime с
// period из поля «Дата» документа и DocID = ID объекта.
func TestObject_MomentTime(t *testing.T) {
	docDate := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	docID := uuid.New()
	o := &Object{
		Type: "Поступление",
		Kind: metadata.KindDocument,
		ID:   docID,
		Fields: map[string]any{
			"дата": docDate,
		},
	}
	v := o.CallMethod("моментвремени", nil)
	mt, ok := v.(*MomentTime)
	if !ok {
		t.Fatalf("ожидался *MomentTime, получили %T", v)
	}
	if !mt.Period.Equal(docDate) {
		t.Errorf("Period: got %v, want %v", mt.Period, docDate)
	}
	if mt.DocID != docID {
		t.Errorf("DocID не совпал")
	}
	if mt.DocType != "Поступление" {
		t.Errorf("DocType: %s", mt.DocType)
	}
}

// При загрузке документа из БД/формы дата приходит СТРОКОЙ. МоментВремени
// должен её распарсить, иначе Period нулевой и moment-фильтр (period < @)
// отсекает все движения → ФИФО «доступно 0».
func TestObject_MomentTime_StringDate(t *testing.T) {
	cases := []string{
		"2026-05-20T12:00:00Z", // RFC3339
		"2026-05-20T12:00:00",  // без зоны
		"2026-05-20 12:00:00",  // SQLite datetime (пробел)
		"2026-05-20",           // только дата
	}
	for _, ds := range cases {
		o := &Object{
			Type:   "Поступление",
			Kind:   metadata.KindDocument,
			ID:     uuid.New(),
			Fields: map[string]any{"дата": ds},
		}
		mt := o.CallMethod("моментвремени", nil).(*MomentTime)
		if mt.Period.IsZero() {
			t.Errorf("дата %q не распарсилась — Period нулевой (ФИФО упал бы)", ds)
		}
	}
}

func TestMomentTime_PointInTime(t *testing.T) {
	docDate := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	docID := uuid.New()
	mt := &MomentTime{Period: docDate, DocID: docID, DocType: "X"}
	p, id := mt.PointInTime()
	if !p.Equal(docDate) {
		t.Errorf("Period: %v vs %v", p, docDate)
	}
	if id != docID.String() {
		t.Errorf("DocID string: %s vs %s", id, docID.String())
	}
}

// Пустое Наименование не должно «выигрывать» — берётся следующее непустое.
func TestObject_String_SkipsEmpty(t *testing.T) {
	o := &Object{
		Type: "X",
		ID:   uuid.New(),
		Fields: map[string]any{
			"наименование": "  ", // только пробелы
			"номер":        "Н-1",
		},
	}
	if got := o.String(); got != "Н-1" {
		t.Errorf("String() = %q, ожидалось «Н-1» (наименование пустое — взять следующее)", got)
	}
}
