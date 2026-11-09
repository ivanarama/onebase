package storage

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
)

// Счётчик записей запроса. Отвечает на вопрос «писал ли этот запрос в базу»:
// событие формы перечитывает строки табличных частей после обработчика,
// только если обработчик что-то записал (мог переписать их через общий
// модуль, #579), — иначе перечитывать незачем, а у заявки колл-центра это 17
// запросов на событие.
//
// Счётчик живёт в контексте, а не в процессе: на живом сервере базу
// постоянно пишут фоновые исполнители (очередь заданий, heartbeat), и общий
// счётчик сдвигался бы на каждом событии. Записи наследуют контекст
// обработчика — и прямые, и в транзакции (BeginTx выводит свой контекст из
// переданного). Учёт с запасом: всё, что не чистый SELECT и не служебная
// команда транзакции, считается записью.

type writeCounterKey struct{}

// WriteCounter — счётчик записей запроса и начала первых операторов (для
// диагностики: что именно писал запрос).
type WriteCounter struct {
	n      atomic.Uint64
	mu     sync.Mutex
	sample []string
}

// Load — сколько изменяющих операторов насчитано.
func (c *WriteCounter) Load() uint64 { return c.n.Load() }

// Sample — начала первых операторов (до writeSampleLimit штук).
func (c *WriteCounter) Sample() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sample...)
}

const writeSampleLimit = 5

// WithWriteCounter добавляет в контекст счётчик записей и возвращает его.
func WithWriteCounter(ctx context.Context) (context.Context, *WriteCounter) {
	c := &WriteCounter{}
	return context.WithValue(ctx, writeCounterKey{}, c), c
}

func countWrite(ctx context.Context, sqlText string) {
	if ctx == nil {
		return
	}
	c, _ := ctx.Value(writeCounterKey{}).(*WriteCounter)
	if c == nil {
		return
	}
	c.n.Add(1)
	c.mu.Lock()
	if len(c.sample) < writeSampleLimit {
		head := strings.Join(strings.Fields(sqlText), " ")
		if len(head) > 48 {
			head = head[:48]
		}
		c.sample = append(c.sample, head)
	}
	c.mu.Unlock()
}

// noteExec учитывает оператор Exec: всё, кроме служебных команд транзакции и
// настроек, считается записью.
func noteExec(ctx context.Context, sqlText string) {
	switch firstSQLWord(sqlText) {
	case "SAVEPOINT", "RELEASE", "BEGIN", "COMMIT", "ROLLBACK", "SET", "PRAGMA", "ANALYZE", "SHOW":
		return
	}
	countWrite(ctx, sqlText)
}

// noteQuery учитывает Query/QueryRow: запись — всё, что не чистый SELECT
// (INSERT … RETURNING, CTE с изменяющими предложениями), по тому же правилу,
// что и режим только чтения (queryAllowed).
func noteQuery(ctx context.Context, sqlText string) {
	if firstSQLWord(sqlText) == "SELECT" && !strings.Contains(sqlText, ";") {
		return
	}
	countWrite(ctx, sqlText)
}

func firstSQLWord(sqlText string) string {
	fields := strings.Fields(sqlText)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(strings.TrimLeft(fields[0], "("))
}
