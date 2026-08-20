package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/storage"
)

// allowNewerSchema — осознанное снятие гейта: открыть базу, обслуженную
// платформой новее этого бинаря. Привязан к постоянному флагу корневой команды
// --allow-newer-schema; переменная окружения нужна там, где флага нет.
var allowNewerSchema bool

func newerSchemaAllowed() bool {
	return allowNewerSchema || os.Getenv(storage.AllowNewerSchemaEnv) != ""
}

// guardSchemaRevision не даёт бинарю работать с базой, которую обслуживала
// платформа новее его самого (issue #1057).
//
// Отказ идёт до первой операции с данными, а не в произвольном месте позже:
// в #1053 старый бинарь дошёл до входа и ответил 500, но с тем же успехом мог
// упасть на проводе документа посреди рабочего дня.
//
// Сбой самой проверки — тоже отказ: гейт, который не смог прочитать ревизию,
// обязан считать базу непроверенной, иначе он защищает ровно до первой
// неполадки связи.
func guardSchemaRevision(ctx context.Context, db *storage.DB) error {
	err := db.CheckSchemaRevision(ctx)
	if err == nil {
		return nil
	}
	var newer *storage.NewerSchemaError
	if errors.As(err, &newer) && newerSchemaAllowed() {
		// Флаг снимает отказ, но не молчание: администратор, который открыл базу
		// старым бинарём осознанно, всё равно должен видеть это в журнале — иначе
		// следующая непонятная ошибка снова будет стоить переписки.
		oblog.Component("cli").Warn("база обслуживалась платформой новее этого бинаря — открыта по явному разрешению",
			"ревизия_базы", newer.Base,
			"ревизия_бинаря", newer.Known,
			"обслужил", newer.UpdatedBy)
		return nil
	}
	return err
}

// stampSchemaRevision поднимает ревизию базы после успешных миграций. Ошибку
// возвращает: не проштампованная база молча теряет защиту следующего запуска,
// а «ошибку игнорируем» на PostgreSQL внутри транзакции всё равно иллюзия.
func stampSchemaRevision(ctx context.Context, db *storage.DB) error {
	if _, err := db.RaiseSchemaRevision(ctx); err != nil {
		return fmt.Errorf("ревизия схемы: %w", err)
	}
	return nil
}
