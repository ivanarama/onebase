package interpreter

// DSL-объект Нумераторы (issue #358). Даёт обработкам и регламентным заданиям
// доступ к тому же атомарному механизму автонумерации, что и REST/UI-путь
// создания записи, чтобы объекты, создаваемые из DSL, получали гарантированно
// уникальный последовательный номер тем же способом, что и объекты из формы/API:
//
//	Новый = Справочники.Договоры.Создать();
//	Новый.Номер = Нумераторы.СледующийНомер("Договоры");
//	Новый.Записать();
//
// Зачем нужен явный вызов: автонумерация (и настроенная через numerator:, и
// legacy-fallback) применяется только на REST/UI-пути записи. Объект, созданный
// из DSL, идёт мимо этих хендлеров и номер сам не получает — как и хук
// ПриЗаписи. Раньше документированного способа взять номер из DSL не было.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// NumeratorStore — то, что объекту Нумераторы нужно от хранилища. Реализуется
// *storage.DB. Обе операции атомарны и безопасны при конкурентных вызовах.
type NumeratorStore interface {
	NextNumber(ctx context.Context, entityName, periodKey string) (int, error)
	NextNum(ctx context.Context, entityName string) (int64, error)
}

// NumeratorRegistry — доступ к метаданным сущности (её настройке numerator).
// Реализуется *runtime.Registry.
type NumeratorRegistry interface {
	GetEntity(name string) *metadata.Entity
}

// NumeratorsRoot — корневой DSL-объект Нумераторы.
type NumeratorsRoot struct {
	ctx   CtxSource
	store NumeratorStore
	reg   NumeratorRegistry
}

// NewNumeratorsRoot создаёт объект для инжекции как DSL extraVar «Нумераторы».
func NewNumeratorsRoot(ctx CtxSource, store NumeratorStore, reg NumeratorRegistry) *NumeratorsRoot {
	return &NumeratorsRoot{ctx: ctx, store: store, reg: reg}
}

// This: у объекта нет доступных членов, только метод. Get/Set — безопасные no-op.
func (r *NumeratorsRoot) Get(string) any  { return nil }
func (r *NumeratorsRoot) Set(string, any) {}

func (r *NumeratorsRoot) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "следующийномер", "nextnumber":
		return r.nextNumber(args)
	}
	panic(userError{Msg: "Нумераторы: неизвестный метод «" + method + "» (доступен СледующийНомер)"})
}

// nextNumber повторяет логику автонумерации REST/UI-пути (generateAutoNumber):
// при настроенном numerator — атомарный счётчик по периоду/области; иначе —
// простая сквозная последовательность (legacy fallback) в формате 000001.
//
//	СледующийНомер("Сущность")        — период по текущей дате;
//	СледующийНомер("Сущность", Дата)  — период по указанной дате;
//	СледующийНомер("Сущность", Поля)  — дата и scope из map/Структура;
//	СледующийНомер("Сущность", Дата, Область) — явный scope.
func (r *NumeratorsRoot) nextNumber(args []any) any {
	if len(args) < 1 || args[0] == nil {
		panic(userError{Msg: `СледующийНомер: укажите имя сущности, напр. СледующийНомер("Договоры")`})
	}
	name := strings.TrimSpace(fmt.Sprintf("%v", args[0]))
	entity := r.reg.GetEntity(name)
	if entity == nil {
		panic(userError{Msg: fmt.Sprintf("СледующийНомер: сущность %q не найдена", name)})
	}
	fields := map[string]any{}
	var explicitScope any
	if len(args) >= 2 {
		switch v := args[1].(type) {
		case time.Time:
			d := v
			fields["дата"] = d
		case map[string]any:
			for k, value := range v {
				fields[k] = unwrapRef(value)
			}
		case *MapThis:
			if v != nil {
				for k, value := range v.M {
					fields[k] = unwrapRef(value)
				}
			}
		default:
			explicitScope = unwrapRef(v)
		}
	}
	if entity.Numerator != nil {
		num := entity.Numerator
		if len(args) >= 3 {
			explicitScope = unwrapRef(args[2])
		}
		if num.Scope != "" {
			hasScope := false
			for field, value := range fields {
				if strings.EqualFold(field, num.Scope) && value != nil && strings.TrimSpace(fmt.Sprint(value)) != "" {
					fields[num.Scope] = value
					if field != num.Scope {
						delete(fields, field)
					}
					hasScope = true
					break
				}
			}
			if !hasScope && explicitScope != nil && strings.TrimSpace(fmt.Sprint(explicitScope)) != "" {
				fields[num.Scope] = explicitScope
				hasScope = true
			}
			if !hasScope {
				panic(userError{Msg: fmt.Sprintf("СледующийНомер: для %s требуется область нумерации %s", entity.Name, num.Scope)})
			}
		}
		periodKey := storage.ComputePeriodKey(num, fields)
		n, err := r.store.NextNumber(r.ctx.Ctx(), entity.Name, periodKey)
		if err != nil {
			panic(userError{Msg: "СледующийНомер: " + err.Error()})
		}
		return storage.FormatNumber(num.Prefix, num.Length, n)
	}
	n, err := r.store.NextNum(r.ctx.Ctx(), entity.Name)
	if err != nil {
		panic(userError{Msg: "СледующийНомер: " + err.Error()})
	}
	s := strconv.FormatInt(n, 10)
	for len(s) < 6 {
		s = "0" + s
	}
	// Legacy-нумератор задаёт минимальную ширину, а не ограничение диапазона:
	// после 999999 номер естественно становится семизначным.
	return s
}
