package storage

import "context"

// Необязательные операции внутри чужой транзакции (issue #826).
//
// На PostgreSQL сбойный запрос переводит ВСЮ транзакцию в состояние aborted:
// СУБД отклоняет каждую следующую команду до отката. Поэтому «ошибку
// игнорируем» для запроса внутри транзакции — иллюзия: игнорируется
// возвращённое значение, а не последствие. Так недоступный журнал аудита ронял
// запись объекта целиком, причём сообщением, по которому причину не найти:
//
//	ERROR: relation "_audit" does not exist
//	ERROR: current transaction is aborted, commands ignored until end of
//	       transaction block
//	commit unexpectedly resulted in rollback
//
// На SQLite такого правила нет: сбойный запрос там локален, поэтому savepoint
// не берётся — лишняя пара команд на каждую запись объекта не нужна.

// bestEffort выполняет необязательную операцию так, чтобы её сбой не утащил за
// собой чужую транзакцию. Ошибку возвращает как есть: решает вызывающий.
func (db *DB) bestEffort(ctx context.Context, fn func(context.Context) error) error {
	if !db.IsPostgres() || !HasTx(ctx) {
		return fn(ctx)
	}
	sp := nextSavepointName("onebase_opt")
	if _, err := db.Exec(ctx, "SAVEPOINT "+sp); err != nil {
		// Транзакция уже мертва — выполнять операцию бессмысленно, но и портить
		// нечего: об этом узнает тот, кто её начал.
		return err
	}
	if err := fn(ctx); err != nil {
		_, _ = db.Exec(ctx, "ROLLBACK TO SAVEPOINT "+sp)
		_, _ = db.Exec(ctx, "RELEASE SAVEPOINT "+sp)
		return err
	}
	_, err := db.Exec(ctx, "RELEASE SAVEPOINT "+sp)
	return err
}
