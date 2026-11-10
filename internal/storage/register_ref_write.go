package storage

import (
	"context"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// regWriteRefValue проверяет значение ссылочного поля регистра перед записью.
//
// Правило то же, что у реквизита объекта (fieldValueDialect): пусто, UUID или
// ссылка. Строка другого вида ссылкой не является — чаще всего это
// представление из результата запроса: поле-ссылка приходит оттуда номером
// документа или наименованием. Раньше регистр принимал такую строку как есть.
// SQLite молча сохранял текст, и измерение указывало в никуда: партия
// поступления, списанная движением с номером вместо ссылки, не уменьшалась
// никогда. PostgreSQL падал ошибкой драйвера о синтаксисе uuid, не называя
// ни регистр, ни поле.
//
// В обменном режиме непредставимая ссылка становится пустой, как у объектов
// (fieldValueForWrite): источник мог работать по другой схеме. Это правило не
// применяется к ключевым измерениям в InfoRegApplyExchange: там сохраняется
// прежняя обработка NOT NULL ключа, включая строковые SQLite-ключи. Значения
// других типов проходят без изменений — их разбирает resolveRefArg, как прежде.
func regWriteRefValue(ctx context.Context, f metadata.Field, v any) (any, error) {
	if f.RefEntity == "" || isNilReferenceValue(v) {
		return v, nil
	}
	checked := v
	if p, ok := refValueAsPointer(v); ok {
		checked = p
	}
	var text string
	switch val := checked.(type) {
	case string:
		text = val
	case uuidProvider:
		text = val.GetRefUUID()
	default:
		return v, nil
	}
	if text == "" {
		return v, nil
	}
	if _, err := uuid.Parse(text); err == nil {
		return v, nil
	}
	if stageModeFromCtx(ctx).Source == StageSourceExchange {
		return nil, nil
	}
	return nil, i18nerr.Wrapf(ErrReferenceTypeMismatch,
		"значение «%s» не является ссылкой на «%s» (похоже на представление: поле-ссылку из результата запроса выбирайте как «Поле.Ссылка»)",
		text, f.RefEntity)
}

// regWriteRefMap применяет regWriteRefValue к значениям полей fields в values и
// возвращает копию с проверенными значениями; исходная карта не меняется.
func regWriteRefMap(ctx context.Context, fields []metadata.Field, values map[string]any) (map[string]any, error) {
	if values == nil {
		return nil, nil
	}
	var out map[string]any
	for _, f := range fields {
		if f.RefEntity == "" {
			continue
		}
		v, ok := values[f.Name]
		if !ok {
			continue
		}
		checked, err := regWriteRefValue(ctx, f, v)
		if err != nil {
			return nil, i18nerr.Wrapf(err, "поле %q", f.Name)
		}
		if out == nil {
			out = make(map[string]any, len(values))
			for k, val := range values {
				out[k] = val
			}
		}
		out[f.Name] = checked
	}
	if out == nil {
		return values, nil
	}
	return out, nil
}
