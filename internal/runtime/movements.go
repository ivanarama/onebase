package runtime

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// MovementsCollector accumulates register movement records written by DSL during OnWrite.
// Accessible in DSL as the "Движения" variable.
type MovementsCollector struct {
	DocType string
	DocID   uuid.UUID
	Period  *time.Time // auto-filled from document's first date field
	pending map[string][]map[string]any
	// persists — сбрасывает ли этот коллектор накопленное в базу. По умолчанию
	// НЕТ, и это сознательно: коллектор подставляется в переменные DSL два
	// десятка раз (регламентное задание, обработка, страница, отчёт, печатная
	// форма, консоль, событие формы…), а сбрасывают его ровно пять путей записи
	// и проведения документа. Раньше разница была невидимой — в остальных
	// контекстах Движения.X.Добавить() выглядел рабочим и молча выбрасывал
	// строки. Теперь опасное состояние надо запрашивать явно (WillPersist), и
	// забытый вызов ломает запись громко, а не портит данные тихо.
	persists bool
}

// WillPersist помечает коллектор как сбрасываемый в базу: вызывающий обязан
// после исполнения хука записать mc.All() (saveMovements/writeMovements).
// Возвращает сам коллектор — удобно в цепочке при создании.
func (mc *MovementsCollector) WillPersist() *MovementsCollector {
	mc.persists = true
	return mc
}

// Persists сообщает, будет ли накопленное записано. Для проверок вызывающих.
func (mc *MovementsCollector) Persists() bool { return mc.persists }

// contextLabels — человекочитаемые имена контекстов, в которых коллектор
// заведомо ничего не пишет. Ключи совпадают с DocType на местах создания.
var contextLabels = map[string]string{
	"scheduler": "регламентное задание",
	"processor": "обработка",
	"report":    "отчёт",
	"page":      "страница",
	"console":   "консоль кода",
	"intake":    "приём сообщений",
	"service":   "HTTP-сервис",
}

// DiscardMessage — текст отказа при попытке накопить движения там, где их
// некому записать. Называет и причину, и куда переносить код. Печатает его
// dslvars: сам runtime прикладных ошибок не возбуждает (иначе пришлось бы
// импортировать интерпретатор, а его тесты уже импортируют runtime).
func (mc *MovementsCollector) DiscardMessage(register string) string {
	where := contextLabels[mc.DocType]
	if where == "" {
		where = "текущий контекст: " + mc.DocType
	}
	return "Движения." + register + ".Добавить(): здесь движения сохранять некуда — " +
		"они принадлежат документу-регистратору и записываются только при его записи или проведении (" + where + "). " +
		"Перенесите формирование движений в ОбработкаПроведения документа."
}

func NewMovementsCollector(docType string, docID uuid.UUID) *MovementsCollector {
	return &MovementsCollector{
		DocType: docType,
		DocID:   docID,
		pending: make(map[string][]map[string]any),
	}
}

func (mc *MovementsCollector) SetPeriod(t time.Time) {
	mc.Period = &t
}

// Get implements interpreter.This — Движения.НазваниеРегистра returns a RegisterMovements.
func (mc *MovementsCollector) Get(name string) any {
	return &RegisterMovements{collector: mc, name: name}
}

func (mc *MovementsCollector) Set(name string, v any) {}

// All returns all pending movements keyed by register name.
func (mc *MovementsCollector) All() map[string][]map[string]any {
	out := make(map[string][]map[string]any, len(mc.pending))
	for k, v := range mc.pending {
		out[k] = v
	}
	return out
}

// RegisterMovements is the per-register movements list returned by Движения.НазваниеРегистра.
type RegisterMovements struct {
	collector *MovementsCollector
	name      string
}

// Get implements interpreter.This (allows member access, though unused directly).
func (rm *RegisterMovements) Get(name string) any    { return nil }
func (rm *RegisterMovements) Set(name string, v any) {}

// movementRow оборачивает строку движения. Ведёт себя как MapThis (член →
// плоский ключ карты в нижнем регистре), но дополнительно поддерживает
// именованную адресацию субконто: Дв.Субконто.<Имя> пишет плоский ключ
// субконто_<имя>, который storage разбирает при записи движения. Краткая форма
// Дв.СубконтоN остаётся обычным членом (ключ субконто<n>).
type movementRow struct{ m map[string]any }

func (r *movementRow) Get(name string) any {
	if strings.EqualFold(name, "Субконто") {
		return &subcontoAccessor{m: r.m}
	}
	low := strings.ToLower(name)
	for k, v := range r.m {
		if strings.ToLower(k) == low {
			return v
		}
	}
	return nil
}

func (r *movementRow) Set(name string, v any) {
	low := strings.ToLower(name)
	for k := range r.m {
		if strings.ToLower(k) == low {
			r.m[k] = v
			return
		}
	}
	r.m[low] = v
}

// subcontoAccessor — объект, возвращаемый Дв.Субконто. Присваивание члену пишет
// в строку движения плоский ключ субконто_<имя>.
type subcontoAccessor struct{ m map[string]any }

func (s *subcontoAccessor) Get(name string) any {
	low := "субконто_" + strings.ToLower(name)
	for k, v := range s.m {
		if strings.ToLower(k) == low {
			return v
		}
	}
	return nil
}

func (s *subcontoAccessor) Set(name string, v any) {
	s.m["субконто_"+strings.ToLower(name)] = v
}

// CallMethod implements interpreter.MethodCallable.
func (rm *RegisterMovements) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "добавить", "add":
		row := make(map[string]any)
		rm.collector.pending[rm.name] = append(rm.collector.pending[rm.name], row)
		return &movementRow{m: row}
	case "очистить", "clear":
		rm.collector.pending[rm.name] = nil
		return nil
	}
	return nil
}
