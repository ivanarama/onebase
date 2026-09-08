package interpreter

import (
	"context"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/typedempty"
)

// ConstantsDB — то, что нужно объекту Константы от хранилища.
type ConstantsDB interface {
	SetConstant(ctx context.Context, name string, value any) error
}

// ConstantsRoot — объект `Константы` в DSL.
//
// Прежде это была обычная карта-снимок (MapThis поверх ListConstants), поэтому
// присваивание `Константы.Имя = Значение` меняло значение ТОЛЬКО в памяти
// текущего процесса: код отрабатывал без ошибки, следующее чтение в том же
// прогоне возвращало новое значение — и всё, в базу не уходило ничего. Ровно на
// этом молча не работало аварийное отключение интеграции по 401: обработка
// «выключить» отчитывалась об успехе, а после перезапуска константа снова была
// включена (#719).
//
// Чтение по-прежнему идёт из снимка, снятого на старте прогона: константы
// читают часто, и лишний запрос на каждое обращение того не стоит. Запись
// уходит в базу сразу и обновляет снимок, поэтому в пределах прогона Get после
// Set видит записанное.
type ConstantsRoot struct {
	ctx              context.Context
	db               ConstantsDB
	names            map[string]string // нижний регистр → объявленное имя
	cache            map[string]any    // объявленное имя → значение
	descriptors      map[string]typedempty.Descriptor
	referenceFactory func(typedempty.Descriptor) any
}

// DeclaredConstant is the immutable metadata needed by the DSL read boundary.
// The full metadata.Constant stays owned by runtime.Registry.
type DeclaredConstant struct {
	Name       string
	Descriptor typedempty.Descriptor
}

// NewConstantsRoot собирает объект Константы. declared — имена из конфигурации,
// values — снимок значений из базы.
func NewConstantsRoot(ctx context.Context, db ConstantsDB, declared []string, values map[string]any) *ConstantsRoot {
	typed := make([]DeclaredConstant, 0, len(declared))
	for _, name := range declared {
		typed = append(typed, DeclaredConstant{Name: name})
	}
	return NewTypedConstantsRoot(ctx, db, typed, values, nil)
}

// NewTypedConstantsRoot builds the production constants object with declared
// types. SQL NULL remains nil in cache and is materialized only by Get.
func NewTypedConstantsRoot(
	ctx context.Context,
	db ConstantsDB,
	declared []DeclaredConstant,
	values map[string]any,
	referenceFactory func(typedempty.Descriptor) any,
) *ConstantsRoot {
	r := &ConstantsRoot{
		ctx:              ctx,
		db:               db,
		names:            make(map[string]string, len(declared)),
		cache:            make(map[string]any, len(values)),
		descriptors:      make(map[string]typedempty.Descriptor, len(declared)),
		referenceFactory: referenceFactory,
	}
	for _, constant := range declared {
		if constant.Name == "" {
			continue
		}
		r.names[strings.ToLower(constant.Name)] = constant.Name
		r.descriptors[constant.Name] = constant.Descriptor
	}
	for k, v := range values {
		// Значение из базы может лежать под именем, которого в конфигурации уже
		// нет (константу удалили) — тогда оно доступно на чтение под своим
		// именем, как и раньше.
		if _, ok := r.names[strings.ToLower(k)]; !ok {
			r.names[strings.ToLower(k)] = k
		}
		r.cache[r.names[strings.ToLower(k)]] = v
	}
	return r
}

func (r *ConstantsRoot) Get(name string) any {
	if canon, ok := r.names[strings.ToLower(name)]; ok {
		raw := r.cache[canon]
		desc, declared := r.descriptors[canon]
		if !declared {
			return raw
		}
		if desc.RefEntity != "" {
			if r.referenceFactory == nil {
				return raw
			}
			value := r.referenceFactory(desc)
			ref, ok := value.(*Ref)
			if !ok || ref == nil {
				return raw
			}
			if raw != nil {
				if uuid, ok := referenceUUID(raw); ok {
					ref.UUID = uuid
				} else {
					ref.UUID = strings.TrimSpace(MatchValueString(raw))
				}
				ref.Name = ref.UUID
			}
			return ref
		}
		return typedempty.Normalize(desc, raw, nil)
	}
	return nil
}

// Set записывает константу в базу и обновляет снимок.
//
// Неизвестное имя — ошибка, а не тихое заведение ключа. Прежде опечатка в имени
// создавала запись в карте, которая жила до конца прогона и никого ни о чём не
// оповещала; отличить «выключил не ту константу» от «выключил не существующую»
// было нельзя ничем.
func (r *ConstantsRoot) Set(name string, v any) {
	canon, ok := r.names[strings.ToLower(name)]
	if !ok {
		RaiseUserError("Константы: неизвестная константа «" + name + "»" + r.hint())
		return
	}
	if r.db == nil {
		RaiseUserError("Константы: запись «" + canon + "» невозможна — нет соединения с базой")
		return
	}
	stored := v
	if desc, declared := r.descriptors[canon]; declared && desc.RefEntity != "" {
		if uuid, ok := referenceUUID(v); ok {
			stored = uuid
		}
	}
	if err := r.db.SetConstant(r.ctx, canon, stored); err != nil {
		RaiseUserError("Константы: запись «" + canon + "»: " + err.Error())
		return
	}
	r.cache[canon] = stored
}

func referenceUUID(value any) (string, bool) {
	ref, ok := value.(interface{ GetRefUUID() string })
	if !ok || value == nil {
		return "", false
	}
	return strings.TrimSpace(ref.GetRefUUID()), true
}

// hint перечисляет объявленные константы: имя ошиблись почти всегда в регистре
// или в раскладке, и список избавляет от похода в конфигурацию.
func (r *ConstantsRoot) hint() string {
	if len(r.names) == 0 {
		return "; в конфигурации не объявлено ни одной константы"
	}
	out := make([]string, 0, len(r.names))
	for _, canon := range r.names {
		out = append(out, canon)
	}
	sort.Strings(out)
	return "; известны: " + strings.Join(out, ", ")
}
