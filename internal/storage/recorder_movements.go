package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// RecorderRegisters — регистры, в которых у регистратора есть движения.
// Порядок внутри каждого среза совпадает с порядком, в котором регистры были
// переданы в RecorderMovementRegisters.
type RecorderRegisters struct {
	Registers        []*metadata.Register
	InfoRegisters    []*metadata.InfoRegister
	AccountRegisters []*metadata.AccountRegister
}

var ErrRecorderLockRequiresTx = errors.New("postgres recorder lock requires active storage transaction")

// LockMovementRecorder сериализует запись/снятие движений до поиска прежних
// строк. Row-lock совместим с обычным Upsert документа: отдельный advisory-key
// после Upsert дал бы обратный порядок «строка → key» / «key → строка».
// FOR NO KEY UPDATE не мешает FK-ссылкам на документ и не меняет его _version.
// На SQLite запись уже сериализована самой транзакцией.
func (db *DB) LockMovementRecorder(ctx context.Context, entity *metadata.Entity, id uuid.UUID) error {
	if !db.IsPostgres() {
		return nil
	}
	if !HasTx(ctx) {
		return ErrRecorderLockRequiresTx
	}
	// У сущностей с этапами Upsert сначала берёт stage-lock, затем row-lock.
	// Сохраняем этот порядок, в том числе для отмены и удаления.
	if stagedEntity(entity) {
		if err := db.lockStageRecord(ctx, entity.Name, id); err != nil {
			return err
		}
	}
	_, err := db.Exec(ctx, "SELECT id FROM "+metadata.TableName(entity.Name)+" WHERE id = $1 FOR NO KEY UPDATE", idArg(db.dialect, id))
	if err != nil {
		return fmt.Errorf("блокировка регистратора %s %s: %w", entity.Name, id, err)
	}
	return nil
}

// RecorderMovementRegisters сообщает, в каких из переданных регистров
// у регистратора есть движения, выполняя поиск ограниченными пакетами.
//
// Нужен проведению: движения документа после записи — ровно то, что
// сформировал его модуль, поэтому прежние движения из регистров, которых модуль
// в этот раз не коснулся, снимаются. Снимать их вслепую из всех регистров
// конфигурации нельзя: запись в регистр с итогами берёт его advisory-лок, и
// каждое проведение запирало бы итоги всех регистров сразу. Отсюда проверка:
// снимаются и запираются только регистры, где движения действительно есть.
//
// Условия те же, что у удаления перед записью: регистр накопления и сведений —
// пара (recorder, recorder_type), регистр бухгалтерии — регистратор. Каждая
// проверка — EXISTS по индексу регистратора.
func (db *DB) RecorderMovementRegisters(ctx context.Context, recorderType string, recorderID uuid.UUID,
	regs []*metadata.Register, infos []*metadata.InfoRegister, accs []*metadata.AccountRegister) (RecorderRegisters, error) {
	d := db.dialect
	var out RecorderRegisters
	// 400 веток укладываются в лимит SQLite (500), а максимум 800 параметров
	// — и в старый лимит SQLite (999). Нумерация параметров начинается заново
	// в каждом запросе; номер регистра остаётся общим для всех пакетов.
	const batchSize = 400
	total := len(regs) + len(infos) + len(accs)
	var found []int
	for start := 0; start < total; start += batchSize {
		end := min(start+batchSize, total)
		var parts []string
		var args []any
		next := func(value any) string {
			args = append(args, value)
			return d.Placeholder(len(args))
		}
		for i := start; i < end; i++ {
			var table string
			switch {
			case i < len(regs):
				table = metadata.RegisterTableName(regs[i].Name)
			case i < len(regs)+len(infos):
				table = metadata.InfoRegTableName(infos[i-len(regs)].Name)
			default:
				table = metadata.AccountRegTableName(accs[i-len(regs)-len(infos)].Name)
			}
			p := next(idArg(d, recorderID))
			var condition string
			if i < len(regs)+len(infos) {
				condition = fmt.Sprintf("recorder = %s AND recorder_type = %s", p, next(recorderType))
			} else {
				condition = "регистратор = " + p
			}
			parts = append(parts, fmt.Sprintf(
				"SELECT %d AS k WHERE EXISTS (SELECT 1 FROM %s WHERE %s)", i, table, condition))
		}
		// Курсор закрывается до следующего запроса: в транзакции PostgreSQL
		// открытый курсор оставляет соединение занятым.
		err := func() error {
			rows, err := db.Query(ctx, strings.Join(parts, " UNION ALL "), args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var k int64
				if err := rows.Scan(&k); err != nil {
					return err
				}
				found = append(found, int(k))
			}
			return rows.Err()
		}()
		if err != nil {
			return out, fmt.Errorf("движения регистратора %s %s: %w", recorderType, recorderID, err)
		}
	}
	// Порядок строк UNION ALL СУБД не обещает — восстанавливаем порядок входа.
	sort.Ints(found)
	for _, i := range found {
		switch {
		case i < len(regs):
			out.Registers = append(out.Registers, regs[i])
		case i < len(regs)+len(infos):
			out.InfoRegisters = append(out.InfoRegisters, infos[i-len(regs)])
		case i < len(regs)+len(infos)+len(accs):
			out.AccountRegisters = append(out.AccountRegisters, accs[i-len(regs)-len(infos)])
		}
	}
	return out, nil
}
