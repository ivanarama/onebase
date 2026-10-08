package storage

// Диагностика нарушения внешнего ключа: после того как драйвер отклонил
// INSERT/UPDATE из-за битой ссылки, назвать поля и значения, которые в
// целевых таблицах не найдены (#1660).
//
// Без этого запись с битой ссылкой отдаёт только «FOREIGN KEY constraint
// failed (787)» — ни поля, ни значения, и связать ошибку с исходной причиной
// (НайтиПоИдентификатору с UUID из выгрузки, интеграции или ввода) можно
// только долгой отладкой.

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// execAllowingFKDiagnosis выполняет запись так, чтобы после нарушения внешнего
// ключа транзакция осталась рабочей: на PostgreSQL любой сбой переводит всю
// транзакцию в aborted и следующий SELECT получил бы «current transaction is
// aborted» вместо данных. bestEffort берёт savepoint только там, где он нужен
// (SQLite сбой и так локален, вне транзакции беречь нечего).
func (db *DB) execAllowingFKDiagnosis(ctx context.Context, sqlText string, args ...any) (CommandTag, error) {
	var tag CommandTag
	err := db.bestEffort(ctx, func(ctx context.Context) error {
		var execErr error
		tag, execErr = db.Exec(ctx, sqlText, args...)
		return execErr
	})
	return tag, err
}

// explainFKViolation дополняет классифицированную ошибку перечислением битых
// ссылок из записываемой строки. Диагностика — best-effort: не смогли
// проверить (нет таблицы, сбой чтения) — возвращаем ошибку как была.
func (db *DB) explainFKViolation(ctx context.Context, entity *metadata.Entity, fields map[string]any, classified error) error {
	diag := db.diagnoseBrokenRefs(ctx, entity, fields)
	if diag == "" {
		return classified
	}
	return fmt.Errorf("%w; %s", classified, diag)
}

// diagnoseBrokenRefs перечисляет ссылочные поля entity, значения которых из
// fields отсутствуют в целевых таблицах. Вызывается только на пути ошибки
// записи, поэтому запросы на горячий путь не ложатся. Пустая строка — «виновных
// не нашли или проверить не удалось», не «битых ссылок нет».
func (db *DB) diagnoseBrokenRefs(ctx context.Context, entity *metadata.Entity, fields map[string]any) string {
	if entity == nil {
		return ""
	}
	d := db.dialect
	var broken []string
	for _, f := range entity.Fields {
		if f.RefEntity == "" {
			continue
		}
		raw, err := fieldValueForWrite(ctx, d, f, fields)
		if err != nil || isNilReferenceValue(raw) {
			// Пустая ссылка пишется NULL и FK нарушить не может; нечитаемое
			// значение отверглось бы раньше FK собственной проверкой.
			continue
		}
		var exists int
		err = db.QueryRow(ctx,
			"SELECT 1 FROM "+metadata.TableName(f.RefEntity)+" WHERE id = "+d.Placeholder(1),
			raw).Scan(&exists)
		if IsNotFound(err) {
			broken = append(broken, fmt.Sprintf("поле %s → %s: ссылка на несуществующий объект %s",
				f.Name, f.RefEntity, refValueString(raw)))
			continue
		}
		if err != nil {
			// Проверять остальные поля на сломанной базе бессмысленно: скажем
			// как раньше, без диагностики.
			return ""
		}
	}
	return strings.Join(broken, "; ")
}

func refValueString(v any) string {
	switch t := v.(type) {
	case uuid.UUID:
		return t.String()
	case string:
		return t
	default:
		return fmt.Sprint(v)
	}
}
