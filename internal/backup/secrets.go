package backup

// Предупреждение о секретах в резервной копии (план 83).
//
// Обычная резервная копия — это копия базы ЦЕЛИКОМ: pg_dump на PostgreSQL,
// VACUUM INTO на SQLite. Отфильтровать из неё отдельные значения нельзя и не
// нужно — восстановление должно давать рабочую базу. Значит, единственный
// способ не увезти секрет в копию — не хранить его в базе значением.
//
// Универсальный экспорт .obz такие ключи не берёт (см. safeSettingKeys), а
// обычная копия берёт всё. Поэтому здесь мы не фильтруем, а предупреждаем: файл
// с открытыми секретами требует того же обращения, что и сами секреты.

import (
	"context"

	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/secrets"
	"github.com/ivantit66/onebase/internal/storage"
)

// PlaintextSecretPaths возвращает пути секретов, лежащих в базе открытым
// текстом. Пустой результат — в копию не уедет ничего лишнего.
func PlaintextSecretPaths(ctx context.Context, db *storage.DB) []string {
	carriers, err := db.SecretCarriers(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, c := range carriers {
		if secrets.Classify(c.Value) == secrets.KindPlain && !secrets.ContainsRef(c.Value) {
			out = append(out, c.Path)
		}
	}
	return out
}

// PlaintextSecretPathsFor открывает базу по параметрам цели бэкапа и возвращает
// пути открытых секретов. Диагностика: любая ошибка молча пропускается — она не
// должна мешать снятию резервной копии.
func PlaintextSecretPathsFor(ctx context.Context, dbType, dsn, sqlitePath string) []string {
	if dsn == "" && sqlitePath == "" {
		return nil
	}
	var (
		db  *storage.DB
		err error
	)
	if dbType == "sqlite" || sqlitePath != "" {
		db, err = storage.ConnectSQLite(ctx, sqlitePath)
	} else {
		db, err = storage.Connect(ctx, dsn)
	}
	if err != nil {
		return nil
	}
	defer db.Close()
	return PlaintextSecretPaths(ctx, db)
}

// WarnPlaintextSecrets — то же, но с записью предупреждения в журнал: для
// автоматического бэкапа, которому некому показать сообщение на экране.
// Интерактивная команда `onebase backup` печатает предупреждение сама и журнал
// не дублирует.
func WarnPlaintextSecrets(ctx context.Context, dbType, dsn, sqlitePath string) {
	paths := PlaintextSecretPathsFor(ctx, dbType, dsn, sqlitePath)
	if len(paths) == 0 {
		return
	}
	oblog.Component("backup").Warn("в резервную копию попадут секреты, лежащие в базе открытым текстом",
		"секретов", len(paths), "пути", paths,
		"как исправить", "onebase secret set <путь> — положить значение зашифрованным")
}
