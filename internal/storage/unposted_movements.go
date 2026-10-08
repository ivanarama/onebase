package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// UnpostedStat — движения, чей документ-регистратор существует, но не проведён.
type UnpostedStat struct {
	RegisterName string
	RecorderType string
	Count        int
}

// UnpostedRecorderMovements находит движения регистров накопления и проводки
// бухрегистров, чей регистратор — проводимый документ со снятым признаком
// проведения.
//
// Проведённость и движения обязаны совпадать: отмена проведения и пометка
// удаления снимают движения вместе с признаком. Расхождение возникает, когда
// признак снял путь, который движений не трогал: так загрузка пакета обмена
// оставляла у приёмника движения документа, который источник распровёл. Остатки
// при этом считают документ, которого в списке проведённых нет, и ни одна
// проверка это не показывала — движения не сироты, регистратор жив.
//
// Смотрятся только документы с posting: true: у непроводимого документа признака
// проведения нет по замыслу, и его «непроведённость» ничего не значит.
func (db *DB) UnpostedRecorderMovements(ctx context.Context, registers []*metadata.Register,
	accountRegisters []*metadata.AccountRegister, entities []*metadata.Entity,
) ([]UnpostedStat, error) {
	d := db.dialect
	docs := postingDocumentTables(entities)
	var stats []UnpostedStat
	sources, err := db.unpostedMovementSources(ctx, registers, accountRegisters)
	if err != nil {
		return nil, err
	}
	for _, src := range sources {
		types, err := db.movementRecorderTypes(ctx, src)
		if err != nil {
			return nil, err
		}
		for _, recType := range types {
			tbl, ok := docs[strings.ToLower(recType)]
			if !ok {
				continue
			}
			exists, err := db.TableExists(ctx, tbl)
			if err != nil {
				return nil, err
			}
			if !exists {
				continue
			}
			var count int
			if err := db.QueryRow(ctx, fmt.Sprintf(
				"SELECT COUNT(*) FROM %s WHERE %s = %s AND %s IN (SELECT id FROM %s WHERE posted = %s)",
				src.table, src.recorderTypeCol, d.Placeholder(1), src.recorderCol, tbl, boolFalseLit(d)),
				recType).Scan(&count); err != nil {
				// «Не смогли посчитать» нельзя отдавать как «ничего не нашли».
				return nil, fmt.Errorf("%s: подсчёт движений непроведённых %s: %w", src.name, recType, err)
			}
			if count > 0 {
				stats = append(stats, UnpostedStat{RegisterName: src.name, RecorderType: recType, Count: count})
			}
		}
	}
	return stats, nil
}

// DeleteUnpostedRecorderMovementsAndRecalcTotals удаляет движения и проводки
// непроведённых документов (см. UnpostedRecorderMovements) и пересчитывает итоги
// в одной транзакции: без пересчёта остатки из итогов не изменились бы.
func (db *DB) DeleteUnpostedRecorderMovementsAndRecalcTotals(ctx context.Context, registers []*metadata.Register,
	accountRegisters []*metadata.AccountRegister, entities []*metadata.Entity,
) (int64, error) {
	d := db.dialect
	docs := postingDocumentTables(entities)
	return db.deleteMovementFamiliesAndRecalcTotals(ctx, registers, accountRegisters,
		func(txCtx context.Context) (int64, error) {
			var total int64
			sources, err := db.unpostedMovementSources(txCtx, registers, accountRegisters)
			if err != nil {
				return 0, err
			}
			for _, src := range sources {
				types, err := db.movementRecorderTypes(txCtx, src)
				if err != nil {
					return total, err
				}
				for _, recType := range types {
					tbl, ok := docs[strings.ToLower(recType)]
					if !ok {
						continue
					}
					exists, err := db.TableExists(txCtx, tbl)
					if err != nil {
						return total, err
					}
					if !exists {
						continue
					}
					ct, err := db.Exec(txCtx, fmt.Sprintf(
						"DELETE FROM %s WHERE %s = %s AND %s IN (SELECT id FROM %s WHERE posted = %s)",
						src.table, src.recorderTypeCol, d.Placeholder(1), src.recorderCol, tbl, boolFalseLit(d)),
						recType)
					if err != nil {
						return total, fmt.Errorf("%s: удаление движений непроведённых %s: %w", src.name, recType, err)
					}
					total += ct.RowsAffected
				}
			}
			return total, nil
		})
}

// postingDocumentTables — таблицы проводимых документов по имени в нижнем регистре.
func postingDocumentTables(entities []*metadata.Entity) map[string]string {
	out := map[string]string{}
	for _, e := range entities {
		if e.Kind == metadata.KindDocument && e.Posting {
			out[strings.ToLower(e.Name)] = metadata.TableName(e.Name)
		}
	}
	return out
}

// movementRecorderTypes читает типы регистраторов источника целиком и закрывает
// курсор до следующих запросов: на единственном SQLite-соединении вложенный
// запрос при открытом курсоре ждал бы соединение вечно.
func (db *DB) movementRecorderTypes(ctx context.Context, src movementSource) ([]string, error) {
	rows, err := db.Query(ctx, fmt.Sprintf("SELECT DISTINCT %s FROM %s", src.recorderTypeCol, src.table))
	if err != nil {
		return nil, fmt.Errorf("%s: чтение типов регистратора: %w", src.name, err)
	}
	defer rows.Close()
	var types []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("%s: чтение типа регистратора: %w", src.name, err)
		}
		if t != RollupRecorderType {
			types = append(types, t)
		}
	}
	return types, rows.Err()
}

// unpostedMovementSources skips only genuinely absent tables. The doctor check
// and fix must propagate schema errors instead of reporting that no work exists.
func (db *DB) unpostedMovementSources(ctx context.Context, registers []*metadata.Register,
	accountRegisters []*metadata.AccountRegister,
) ([]movementSource, error) {
	sources := make([]movementSource, 0, len(registers)+len(accountRegisters))
	for _, reg := range registers {
		sources = append(sources, movementSource{
			name: reg.Name, table: metadata.RegisterTableName(reg.Name),
			recorderCol: "recorder", recorderTypeCol: "recorder_type",
		})
	}
	for _, reg := range accountRegisters {
		sources = append(sources, movementSource{
			name: reg.Name, table: metadata.AccountRegTableName(reg.Name),
			recorderCol: "регистратор", recorderTypeCol: "регистратор_тип",
		})
	}
	out := make([]movementSource, 0, len(sources))
	for _, src := range sources {
		exists, err := db.TableExists(ctx, src.table)
		if err != nil {
			return nil, err
		}
		if exists {
			out = append(out, src)
		}
	}
	return out, nil
}
