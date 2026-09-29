package storage

import (
	"context"
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

// RecorderMovementRegisters отвечает одним запросом, в каких из переданных
// регистров у регистратора есть движения.
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
	var (
		parts []string
		args  []any
		n     int
	)
	next := func() string {
		n++
		return d.Placeholder(n)
	}
	// Номер ветки UNION однозначно указывает на регистр: сначала накопление,
	// затем сведения, затем бухгалтерия — в порядке переданных срезов.
	addPair := func(table string) {
		p1 := next()
		p2 := next()
		parts = append(parts, fmt.Sprintf(
			"SELECT %d AS k WHERE EXISTS (SELECT 1 FROM %s WHERE recorder = %s AND recorder_type = %s)",
			len(parts), table, p1, p2))
		args = append(args, idArg(d, recorderID), recorderType)
	}
	for _, reg := range regs {
		addPair(metadata.RegisterTableName(reg.Name))
	}
	for _, ir := range infos {
		addPair(metadata.InfoRegTableName(ir.Name))
	}
	for _, ar := range accs {
		p := next()
		parts = append(parts, fmt.Sprintf(
			"SELECT %d AS k WHERE EXISTS (SELECT 1 FROM %s WHERE регистратор = %s)",
			len(parts), metadata.AccountRegTableName(ar.Name), p))
		args = append(args, idArg(d, recorderID))
	}
	var out RecorderRegisters
	if len(parts) == 0 {
		return out, nil
	}
	rows, err := db.Query(ctx, strings.Join(parts, " UNION ALL "), args...)
	if err != nil {
		return out, fmt.Errorf("движения регистратора %s %s: %w", recorderType, recorderID, err)
	}
	defer rows.Close()
	var found []int
	for rows.Next() {
		var k int64
		if err := rows.Scan(&k); err != nil {
			return out, fmt.Errorf("движения регистратора %s %s: %w", recorderType, recorderID, err)
		}
		found = append(found, int(k))
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("движения регистратора %s %s: %w", recorderType, recorderID, err)
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
