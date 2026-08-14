package storage

import (
	"context"

	"github.com/ivantit66/onebase/internal/metadata"
)

// movementSource — таблица движений и имена колонок регистратора.
//
// Регистр накопления и регистр бухгалтерии хранят движения одинаково по смыслу
// и по-разному по именам: recorder/recorder_type против регистратор/
// регистратор_тип. Из-за этого проверки doctor, написанные под накопление,
// бухрегистр не видели вовсе (#881): проводки удалённого документа оставались
// в акк_* навсегда, а обороты — перекошенными на их сумму.
//
// Дескриптор заведён, чтобы починка не превратилась в пятую копию логики.
// Копия разошлась бы с оригиналом именно в тонкости, ради которой он написан:
// «тип регистратора неизвестен конфигурации» — это НЕ «документ удалён»
// (переименование даёт то же расхождение), и такие движения удалять нельзя.
type movementSource struct {
	name            string // имя регистра — для сообщений об ошибках
	table           string
	recorderCol     string
	recorderTypeCol string
}

// accumSources — источники движений регистров накопления. Немигрированные
// таблицы отбрасываются здесь, а не в каждой проверке отдельно.
func (db *DB) accumSources(ctx context.Context, registers []*metadata.Register) []movementSource {
	out := make([]movementSource, 0, len(registers))
	for _, reg := range registers {
		table := metadata.RegisterTableName(reg.Name)
		if !db.HasTable(ctx, table) {
			continue
		}
		out = append(out, movementSource{
			name: reg.Name, table: table,
			recorderCol: "recorder", recorderTypeCol: "recorder_type",
		})
	}
	return out
}

// accountSources — источники проводок регистров бухгалтерии.
func (db *DB) accountSources(ctx context.Context, regs []*metadata.AccountRegister) []movementSource {
	out := make([]movementSource, 0, len(regs))
	for _, reg := range regs {
		table := metadata.AccountRegTableName(reg.Name)
		if !db.HasTable(ctx, table) {
			continue
		}
		out = append(out, movementSource{
			name: reg.Name, table: table,
			recorderCol: "регистратор", recorderTypeCol: "регистратор_тип",
		})
	}
	return out
}
