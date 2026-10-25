package interpreter

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// UUIDValue — значение типа УникальныйИдентификатор, как в 1С:
//
//	Ключ = Новый УникальныйИдентификатор();                 // случайный, версия 4
//	Ключ = Новый УникальныйИдентификатор("1d2c…-…");        // разбор строки
//
// Нужен там, где модуль сам выдаёт идентификатор: ключ идемпотентности запроса
// к внешнему сервису (Idempotency-Key на каждое нажатие «Позвонить»), метка
// пакета обмена. Строка(Ключ) — канонический вид в нижнем регистре; в
// ЗаписатьJSON и параметрах фонового задания значение уходит строкой и
// возвращается в задание тем же типом.
//
// Метод ссылки Ссылка.УникальныйИдентификатор() по-прежнему возвращает строку:
// на нём стоят существующие конфигурации.
type UUIDValue struct{ id uuid.UUID }

// NewUUIDValue оборачивает идентификатор в значение DSL (для хостов: параметры
// фонового задания приходят из хранилища как uuid.UUID).
func NewUUIDValue(id uuid.UUID) UUIDValue { return UUIDValue{id: id} }

// UUID — идентификатор для хоста.
func (u UUIDValue) UUID() uuid.UUID { return u.id }

func (u UUIDValue) String() string { return u.id.String() }

// TypeName — имя типа для ТипЗнч().
func (u UUIDValue) TypeName() string { return "УникальныйИдентификатор" }

func (u UUIDValue) MarshalJSON() ([]byte, error) { return json.Marshal(u.id.String()) }

// newUUIDValue — конструктор «Новый УникальныйИдентификатор([Строка])». Строка
// принимается только в каноническом виде xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
// (регистр не важен): фигурные скобки и «urn:uuid:» uuid.Parse пропустил бы,
// и одна и та же метка писалась бы в журналы по-разному.
func newUUIDValue(args []any) UUIDValue {
	if len(args) == 0 || args[0] == nil {
		id, err := uuid.NewRandom()
		if err != nil {
			RaiseUserError("УникальныйИдентификатор: не удалось получить случайное значение: " + err.Error())
		}
		return UUIDValue{id: id}
	}
	if len(args) > 1 {
		RaiseUserError(fmt.Sprintf("Новый УникальныйИдентификатор: ожидается не больше одного аргумента, получено %d", len(args)))
	}
	switch v := unwrapReadOnly(args[0]).(type) {
	case UUIDValue:
		return v
	case string:
		text := strings.TrimSpace(v)
		id, err := uuid.Parse(text)
		if err != nil || len(text) != 36 {
			RaiseUserError(fmt.Sprintf("Новый УникальныйИдентификатор: «%s» — не уникальный идентификатор; ожидается вид xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", v))
		}
		return UUIDValue{id: id}
	}
	RaiseUserError(fmt.Sprintf("Новый УникальныйИдентификатор: аргумент должен быть строкой, получено %s", getTypeName(args[0])))
	return UUIDValue{}
}
