package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Заглушка маски ПДн не должна становиться данными.
//
// Под маской (field_access: mask_all, mask_tail, mask_city) модуль читает не
// значение, а заглушку «••••••». Ничто не мешало присвоить её реквизиту
// ДРУГОГО объекта и записать: так в базу оператора КЦ попали телефоны
// «••••••» в заявках, скопированных модулем из звонка. Форма такую запись
// отсекала давно (access.MaskedEmptyFillable), но модуль, REST и обмен идут
// мимо формы. Поэтому страховка стоит здесь, на всех путях записи объекта, как
// enumBackstop.
//
// Уже лежащая в базе заглушка запись не блокирует: документ, испорченный до
// этой проверки, должен по-прежнему записываться (исправить его телефон —
// отдельная правка). Отказ — только когда заглушка появляется в реквизите
// заново или меняется.

// ErrMaskedValueWrite — запись отклонена: в реквизите заглушка маски вместо
// значения.
var ErrMaskedValueWrite = errors.New("скрытое значение нельзя записать — возьмите настоящее значение на сервере")

// MaskFixed — заглушка mask_all и хвост mask_city; MaskBullet — символ маски.
// Их же использует access.MaskValue: одна пара констант на оба пакета.
const (
	MaskFixed  = "••••••"
	MaskBullet = "•"
)

// IsMaskPlaceholder сообщает, что строка — ровно то, что платформа
// подставляет вместо значения под маской: «••••••» (mask_all), точки с
// открытым хвостом «•••••1122» (mask_tail) или «Город, ••••••» (mask_city).
// Точка внутри обычного текста заглушкой не считается.
func IsMaskPlaceholder(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasSuffix(s, ", "+MaskFixed) {
		return true
	}
	rest := strings.TrimLeft(s, MaskBullet)
	return rest != s && !strings.Contains(rest, MaskBullet)
}

// maskBackstop отклоняет запись шапки объекта, в строковом реквизите которой
// заглушка маски, если в базе у этого объекта там лежит другое значение.
func (db *DB) maskBackstop(ctx context.Context, entity *metadata.Entity, id uuid.UUID, fields map[string]any) error {
	if entity == nil || len(fields) == 0 || stageModeFromCtx(ctx).Source == StageSourceExchange {
		return nil
	}
	var suspects []metadata.Field
	for _, f := range entity.Fields {
		if maskableField(f) && IsMaskPlaceholder(valueByNameFold(fields, f.Name)) {
			suspects = append(suspects, f)
		}
	}
	if len(suspects) == 0 {
		return nil
	}
	stored := map[string]any{}
	if id != uuid.Nil {
		rows, err := db.GetFieldsByIDs(ctx, entity, []uuid.UUID{id}, suspects)
		if err != nil {
			return err
		}
		if row, ok := rows[id.String()]; ok {
			stored = row
		}
	}
	for _, f := range suspects {
		if fmt.Sprint(valueByNameFold(fields, f.Name)) == fmt.Sprint(valueByNameFold(stored, f.Name)) {
			continue
		}
		return fmt.Errorf("%s.%s: %w", entity.Name, f.Name, ErrMaskedValueWrite)
	}
	return nil
}

// maskBackstopRows — та же страховка для строк табличной части. Строки
// перезаписываются целиком, поэтому «уже лежит в базе» — значение есть в этой
// колонке у строк того же объекта.
func (db *DB) maskBackstopRows(ctx context.Context, entityName string, tp metadata.TablePart, parentID uuid.UUID, rows []map[string]any) error {
	if len(rows) == 0 || stageModeFromCtx(ctx).Source == StageSourceExchange {
		return nil
	}
	for _, f := range tp.Fields {
		if !maskableField(f) {
			continue
		}
		var existing map[string]bool
		for i, row := range rows {
			value := valueByNameFold(row, f.Name)
			if !IsMaskPlaceholder(value) {
				continue
			}
			if existing == nil {
				var err error
				if existing, err = db.tablePartColumnValues(ctx, entityName, tp, f, parentID); err != nil {
					return err
				}
			}
			if existing[fmt.Sprint(value)] {
				continue
			}
			return fmt.Errorf("%s.%s[%d].%s: %w", entityName, tp.Name, i+1, f.Name, ErrMaskedValueWrite)
		}
	}
	return nil
}

// maskableField — строковый нессылочный реквизит: маскируются только строки.
func maskableField(f metadata.Field) bool {
	return strings.TrimSpace(f.RefEntity) == "" && f.Type == metadata.FieldTypeString
}

func (db *DB) tablePartColumnValues(ctx context.Context, entityName string, tp metadata.TablePart, f metadata.Field, parentID uuid.UUID) (map[string]bool, error) {
	out := map[string]bool{}
	if parentID == uuid.Nil {
		return out, nil
	}
	table := metadata.TablePartTableName(entityName, tp.Name)
	rows, err := db.Query(ctx, fmt.Sprintf("SELECT %s FROM %s WHERE parent_id = %s", metadata.ColumnName(f), table, db.dialect.Placeholder(1)), idArg(db.dialect, parentID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v *string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if v != nil {
			out[*v] = true
		}
	}
	return out, rows.Err()
}
