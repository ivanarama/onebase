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
// ВАЖНОЕ ПРАВИЛО: при создании проверяется полный набор, а при частичной правке
// отсутствие ключа означает «не меняем», а не «стёрли». Storage читает текущую
// строку и проверяет итоговый снимок (старые значения + переданные изменения),
// поэтому пропуск ключа не обнуляет required-реквизит, а явная очистка всё равно
// отклоняется. Object-level пути дополнительно валидируют ТЧ после auto-number,
// preflight и OnWrite/OnPost, непосредственно перед финальной записью.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// ErrRequiredFieldEmpty — запись отклонена: обязательный реквизит пуст.
// Типизирована, чтобы вызывающий отличал её от сбоя БД.
var ErrRequiredFieldEmpty = errors.New("реквизит обязателен к заполнению")

// requiredBackstop отклоняет запись с незаполненным обязательным реквизитом.
// Вызывающий передаёт полный effective map (старые значения + присланные
// изменения), поэтому отсутствие required-ключа здесь всегда является ошибкой.
// Обмен исключён по той же причине, что и у перечислений: узел-приёмник может
// работать на другой версии конфигурации, где обязательности ещё нет.
func (db *DB) requiredBackstop(ctx context.Context, entity *metadata.Entity, fields map[string]any) error {
	if entity == nil {
		return nil
	}
	if stageModeFromCtx(ctx).Source == StageSourceExchange {
		return nil
	}
	if msg := ValidateRequiredValues(entity, fields, true); msg != "" {
		return fmt.Errorf("%w: %s", ErrRequiredFieldEmpty, msg)
	}
	return nil
}

// requiredBackstopRows прикрывает отдельный writer табличных частей. Строка ТЧ
// всегда передаётся целиком, поэтому каждый required-реквизит обязан
// присутствовать. Проверка выполняется ДО DELETE старых строк.
func (db *DB) requiredBackstopRows(ctx context.Context, entityName string, tp metadata.TablePart, rows []map[string]any) error {
	if stageModeFromCtx(ctx).Source == StageSourceExchange {
		return nil
	}
	if msg := ValidateRequiredTablePartValues(entityName, tp, rows); msg != "" {
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
		// canonicalFieldValue ищет ключ без учёта регистра: формы кладут
		// PascalCase, Object.Set — lowercase. Второе значение отличает «нет ключа» от
		// «ключ с пустым значением», и это здесь принципиально.
		value, given := canonicalFieldValue(fields, f.Name)
		if !given {
			if requireAll {
				return requiredFieldMessage(entity.Name + "." + f.Name)
			}
			continue
		}
		if isBlankRequiredFieldValue(f, value) {
			return requiredFieldMessage(entity.Name + "." + f.Name)
		}
	}
	return ""
}

// ValidateRequiredTablePartValues проверяет полный набор значений каждой
// переданной строки табличной части. Наличие самой строки не обязательно, но
// если строка есть, required-реквизиты в ней не могут быть пустыми.
func ValidateRequiredTablePartValues(entityName string, tp metadata.TablePart, rows []map[string]any) string {
	for i, row := range rows {
		for _, f := range tp.Fields {
			if !f.Required {
				continue
			}
			value, given := canonicalFieldValue(row, f.Name)
			if !given || isBlankRequiredFieldValue(f, value) {
				return requiredFieldMessage(fmt.Sprintf("%s.%s[%d].%s", entityName, tp.Name, i+1, f.Name))
			}
		}
	}
	return ""
}

// ValidateRequiredObjectValues — итоговая проверка объекта после
// автонумерации, preflight, enrichment и OnWrite/OnPost. Отсутствующая ТЧ
// означает «не меняем»; переданная ТЧ является полной заменой строк.
func ValidateRequiredObjectValues(entity *metadata.Entity, fields map[string]any,
	tpRows map[string][]map[string]any, requireAll bool) string {
	if msg := ValidateRequiredValues(entity, fields, requireAll); msg != "" {
		return msg
	}
	if entity == nil {
		return ""
	}
	for _, tp := range entity.TableParts {
		rows, given := tpRows[tp.Name]
		if !given {
			continue
		}
		if msg := ValidateRequiredTablePartValues(entity.Name, tp, rows); msg != "" {
			return msg
		}
	}
	return ""
}

func requiredFieldMessage(path string) string {
	return path + ": реквизит обязателен к заполнению"
}

// isBlankRequiredFieldValue учитывает фактическую коэрцию ссылки в storage.
// Непустая строка, которая не является UUID, раньше проходила generic-проверку,
// а fieldValueDialect затем превращал её в NULL — required-инвариант нарушался.
func isBlankRequiredFieldValue(f metadata.Field, value any) bool {
	if f.RefEntity != "" {
		// fieldValueDialect принимает и значение ссылочной структуры, чей
		// GetRefUUID объявлен на pointer receiver. Проверка обязана видеть его
		// точно так же, иначе валидная DSL-ссылка была бы отвергнута только из-за
		// способа упаковки в interface{}.
		if pointer, ok := refValueAsPointer(value); ok {
			value = pointer
		}
		id, err := uuid.Parse(refUUIDString(value))
		return err != nil || id == uuid.Nil
	}
	return isBlankRequiredValue(value)
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

// effectiveEntityValues строит снимок итоговых прикладных реквизитов, не
// мутируя caller map. Он нужен проверке required, FTS и аудиту при частичной
// записи: отсутствующий ключ сохраняет значение из БД.
func effectiveEntityValues(entity *metadata.Entity, old, incoming map[string]any) map[string]any {
	if entity == nil {
		return incoming
	}
	out := make(map[string]any, len(entity.Fields))
	for _, f := range entity.Fields {
		if value, given := canonicalFieldValue(incoming, f.Name); given {
			out[f.Name] = value
			continue
		}
		if value, given := canonicalFieldValue(old, f.Name); given {
			out[f.Name] = value
		}
	}
	return out
}

func hasRequiredEntityFields(entity *metadata.Entity) bool {
	if entity == nil {
		return false
	}
	for _, f := range entity.Fields {
		if f.Required {
			return true
		}
	}
	return false
}
