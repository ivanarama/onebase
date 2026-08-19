package storage

// Обязательность реквизита при записи (#1033).
//
// До этого обязательность можно было выразить только у константы: у реквизита
// сущности ключ `required: true` молча игнорировался загрузчиком, а линт
// объявлял его неизвестным — то есть конфигурация писала правило, которого нет,
// и вдобавок не проходила собственную проверку.
//
// Проверка живёт там же, где страховка значений перечислений: у записи, а не у
// входа. Причина та же — путей записи несколько, и гарантия, поставленная у
// одного, на остальных отсутствует (#962, Н3).
//
// ВАЖНОЕ ПРАВИЛО: проверяются только те реквизиты, которые ЕСТЬ в наборе
// значений. Отсутствие ключа означает «не меняем», а не «стёрли»: частичная
// запись — обычное дело (обновили одно поле, служебная правка, миграция), и
// требовать в ней полный набор значило бы сломать рабочие сценарии. Полноту при
// создании проверяет вызывающий, у которого объект целиком (entityservice.Save).

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ErrRequiredFieldEmpty — запись отклонена: обязательный реквизит пуст.
// Типизирована, чтобы вызывающий отличал её от сбоя БД.
var ErrRequiredFieldEmpty = errors.New("реквизит обязателен к заполнению")

// requiredBackstop отклоняет запись, в которой обязательный реквизит передан
// пустым. Обмен исключён по той же причине, что и у перечислений: узел-приёмник
// может работать на другой версии конфигурации, где обязательности ещё нет, и
// рвать репликацию из-за этого дороже, чем принять запись (#1037).
func (db *DB) requiredBackstop(ctx context.Context, entity *metadata.Entity, fields map[string]any) error {
	if entity == nil {
		return nil
	}
	if stageModeFromCtx(ctx).Source == StageSourceExchange {
		return nil
	}
	if msg := ValidateRequiredValues(entity, fields, false); msg != "" {
		return fmt.Errorf("%w: %s", ErrRequiredFieldEmpty, msg)
	}
	return nil
}

// ValidateRequiredValues проверяет непустоту переданных обязательных
// реквизитов. Возвращает текст пользовательской ошибки либо пустую строку.
//
// requireAll = true — режим создания: обязательный реквизит должен быть и
// заполнен, и присутствовать в наборе.
func ValidateRequiredValues(entity *metadata.Entity, fields map[string]any, requireAll bool) string {
	if entity == nil {
		return ""
	}
	for _, f := range entity.Fields {
		if !f.Required {
			continue
		}
		// lookupFieldValue ищет ключ без учёта регистра: формы кладут PascalCase,
		// Object.Set — lowercase. Второе значение отличает «нет ключа» от
		// «ключ с пустым значением», и это здесь принципиально.
		value, given := lookupFieldValue(fields, f.Name)
		if !given {
			if requireAll {
				return fmt.Sprintf("%s.%s: реквизит обязателен к заполнению", entity.Name, f.Name)
			}
			continue
		}
		if isBlankRequiredValue(value) {
			return fmt.Sprintf("%s.%s: реквизит обязателен к заполнению", entity.Name, f.Name)
		}
	}
	return ""
}

// isBlankRequiredValue — что считается незаполненным. Ноль и Ложь заполнены:
// это осмысленные значения, и объявлять их пустыми значило бы запретить
// «Скидка = 0» у обязательного реквизита.
func isBlankRequiredValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		s := strings.TrimSpace(t)
		return s == "" || s == "<nil>"
	}
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	return s == "" || s == "<nil>"
}

// lookupFieldValue достаёт значение поля по имени без учёта регистра ключа:
// формы кладут PascalCase, Object.Set — lowercase. Второй результат отличает
// «ключа нет» от «ключ есть, значение пустое» — для обязательности это разные
// случаи.
//
// В PR #1045 (иерархия) появляется такой же по смыслу помощник для служебных
// полей; когда обе ветки войдут, их стоит свести в один — здесь дубль оставлен
// намеренно, чтобы ветки не конфликтовали на пустом месте.
func lookupFieldValue(fields map[string]any, name string) (any, bool) {
	if len(fields) == 0 {
		return nil, false
	}
	if v, ok := fields[name]; ok {
		return v, true
	}
	for k, v := range fields {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return nil, false
}
