package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

type Object struct {
	Type string
	Kind metadata.Kind
	// Presentation carries the entity-level explicit label candidates into
	// DSL's Строка(ЭтотОбъект). Object intentionally does not retain the whole
	// metadata graph, but without these names String() would bypass #846 and
	// keep using Наименование/Номер inside hooks.
	Presentation  []string
	ID            uuid.UUID
	Fields        map[string]any
	TablePartRows map[string][]map[string]any
}

func NewObject(entityType string, kind metadata.Kind) *Object {
	return &Object{
		Type:          entityType,
		Kind:          kind,
		ID:            uuid.New(),
		Fields:        make(map[string]any),
		TablePartRows: make(map[string][]map[string]any),
	}
}

// EnsureTableParts заводит ключи для всех объявленных табличных частей.
//
// Без этого у ПУСТОЙ табличной части ключа в TablePartRows нет вовсе, и
// `this.Товары` в обработчике — «Неопределено». То есть типовая проверка
// «если строк нет — исключение» ломалась ровно в том случае, ради которого
// написана (issue #842). entityservice ключи заводит сам, читая ТЧ из БД; на
// пути `Документы.X.Создать()` их заводит вызывающий перед запуском хука.
func (o *Object) EnsureTableParts(e *metadata.Entity) {
	if o == nil || e == nil {
		return
	}
	o.Presentation = append(o.Presentation[:0], e.Presentation...)
	if o.TablePartRows == nil {
		o.TablePartRows = make(map[string][]map[string]any, len(e.TableParts))
	}
	for _, tp := range e.TableParts {
		found := false
		for k := range o.TablePartRows {
			if strings.EqualFold(k, tp.Name) {
				found = true
				break
			}
		}
		if !found {
			o.TablePartRows[tp.Name] = nil
		}
	}
}

func (o *Object) Get(name string) any {
	name = strings.ToLower(name)
	if o.TablePartRows != nil {
		for k := range o.TablePartRows {
			if strings.ToLower(k) == name {
				// Не сырой срез: у него нет методов, и `this.Товары.Количество()`
				// падал, хотя `Для Каждого … Из this.Товары` работал (issue
				// #842). Обёртка адресует те же строки, не копируя их.
				return &TablePart{obj: o, name: k}
			}
		}
	}
	for k, v := range o.Fields {
		if strings.ToLower(k) == name {
			return v
		}
	}
	return nil
}

func (o *Object) Set(name string, v any) {
	if o.Fields == nil {
		o.Fields = make(map[string]any)
	}
	found := false
	for key := range o.Fields {
		if strings.EqualFold(key, name) {
			// Preserve the key spelling supplied by the caller/metadata. Storage,
			// audit and JSON serializers may use that canonical spelling. If an
			// older map already contains duplicate case variants, keep their values
			// consistent instead of leaving one stale and order-dependent.
			o.Fields[key] = v
			found = true
		}
	}
	if !found {
		o.Fields[strings.ToLower(name)] = v
	}
}

// GetRefUUID — реализует тот же интерфейс, что и *interpreter.Ref.
// Нужно для записи Object в reference:*-колонки регистра без
// двойной диспетчеризации в storage (см. замечание #17 и
// «unsupported type runtime.Object, a struct» при проведении).
func (o *Object) GetRefUUID() string {
	if o == nil {
		return ""
	}
	return o.ID.String()
}

// MomentTime — снимок «момента времени» для виртуальных таблиц регистров
// ( Передаётся в .Остатки/.Обороты/.СрезПоследних как
// первый аргумент и обрабатывается query-translator'ом:
//
//	WHERE period < @Period OR (period = @Period AND recorder != @DocID)
//
// то есть «всё что было ДО этой документной строки». При перепроведении
// задним числом это даёт корректные остатки — текущий документ исключается
// из своей же сводки.
type MomentTime struct {
	Period  time.Time
	DocID   uuid.UUID
	DocType string // recorder_type для accumulation register
}

// PointInTime реализует контракт, который ищет query-translator (без импорта
// runtime). Возвращает значение Period и string-UUID документа.
func (m *MomentTime) PointInTime() (time.Time, string) {
	if m == nil {
		return time.Time{}, ""
	}
	return m.Period, m.DocID.String()
}

// CallMethod implements interpreter.MethodCallable so DSL can call
// `ЭтотОбъект.МоментВремени()` and `ЭтотОбъект.Дата` style ergonomics.
// Currently the only method is МоментВремени — returns *MomentTime
// initialized from the object's date field.
func (o *Object) CallMethod(method string, args []any) any {
	switch method {
	case "моментвремени", "pointintime":
		var p time.Time
		// первое непустое date-поле. o.Get — регистронезависимый поиск:
		// ключи Fields приходят то в PascalCase (из БД/формы), то в lower-case
		// (после Object.Set), поэтому прямой o.Fields[k] промахивался.
		for _, k := range []string{"дата", "date", "период", "period"} {
			if t := AsTime(o.Get(k)); !t.IsZero() {
				p = t
				break
			}
		}
		return &MomentTime{Period: p, DocID: o.ID, DocType: o.Type}
	}
	return nil
}

// AsTime приводит значение date-поля к time.Time. Значение может быть
// time.Time (PostgreSQL / распарсенная форма) либо строкой RFC3339
// (SQLite хранит дату как text — см. storage.crud). Возвращает нулевое
// время, если значение пустое или нераспознано.
func AsTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case *time.Time:
		if t != nil {
			return *t
		}
	case string:
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05-07:00", "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02"} {
			if parsed, err := time.ParseInLocation(layout, t, time.Local); err == nil {
				return parsed
			}
		}
	}
	return time.Time{}
}

// String — display-имя объекта для записи в string-колонки регистра
// и DSL-функцию Строка(). При явном presentation перебираем его кандидаты;
// без ключа сохраняем прежнюю конвенцию Наименование/Name/Номер/Number.
func (o *Object) String() string {
	if o == nil {
		return ""
	}
	if len(o.Presentation) > 0 {
		for _, name := range o.Presentation {
			if value := o.Get(name); value != nil {
				if label := strings.TrimSpace(fmt.Sprint(value)); label != "" {
					return label
				}
			}
		}
		return o.shortStringID()
	}
	for _, k := range []string{"наименование", "name", "номер", "number"} {
		// Fields приходят как в lowercase после Object.Set, так и в PascalCase
		// из БД/формы существующего объекта. Get ищет регистронезависимо.
		if v := o.Get(k); v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" {
				return s
			}
		}
	}
	// fallback — короткий хвост UUID, чтобы не путаться при отладке
	return o.shortStringID()
}

func (o *Object) shortStringID() string {
	id := o.ID.String()
	if len(id) >= 8 {
		return o.Type + ":" + id[:8]
	}
	return o.Type + ":" + id
}
