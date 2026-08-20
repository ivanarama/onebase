package storage

import (
	"context"
	"fmt"
)

// Служебные таблицы платформы (issue #827).
//
// Раньше каждый вызывающий перечислял их сам, и списки разъезжались: сервер
// заводил один набор, procrun другой, а тестовый хелпер — вовсе никакой. Из-за
// последнего матричные тесты на PostgreSQL были зелены только благодаря
// ПОРЯДКУ прогона: эфемерная схема пуста, но search_path подхватывает public,
// куда служебные таблицы клал предыдущий пакет. Достаточно было запустить один
// пакет отдельно — и он падал лавиной непонятных ошибок, ни одна из которых не
// называла причину.
//
// Ошибка любой части — ошибка целиком: молча пропустить таблицу значит
// вернуться к тому же классу дефектов, только тише.

// EnsureServiceSchema создаёт служебные таблицы, без которых платформа
// работает лишь частично: журнал аудита, настройки, счётчики нумерации,
// история этапов, обмен, приёмка, журнал веб-хуков, пресеты отчётов.
func (db *DB) EnsureServiceSchema(ctx context.Context) error {
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		// Ревизия схемы (#1057) идёт первой: это удостоверение базы, а не одна из
		// её подсистем. Таблицу заводим, но не штампуем — ревизию поднимают
		// только пути, которые действительно привели схему в соответствие
		// (run/migrate/deploy/dev).
		{"schema revision", db.EnsureSchemaRevisionSchema},
		{"audit", db.EnsureAuditSchema},
		{"settings", db.EnsureSettingsSchema},
		{"numerator", db.EnsureNumeratorSchema},
		{"stage history", db.EnsureStageHistorySchema},
		{"exchange", db.EnsureExchangeSchema},
		{"intake", db.EnsureIntakeSchema},
		{"webhook log", db.EnsureWebhookLogSchema},
		{"report presets", db.EnsureReportPresetSchema},
		// Журнал прогонов регламентных заданий: с планом 123 его читают из
		// прикладного кода, поэтому таблица нужна везде, где нужны служебные, —
		// в том числе в матричных тестах, которые поднимают базу отсюда (#827).
		{"scheduled runs", db.EnsureScheduledRunsTable},
		// Очередь фоновых заданий (план 130): её читают и прикладной код, и
		// монитор админки, поэтому таблица нужна везде, где нужны служебные.
		// Порядок здесь свободный — на другие служебные таблицы она не
		// ссылается, в отличие от блока публикаций ниже.
		{"job queue", db.EnsureJobQueueSchema},
		// Публикации файлов (план 127): таблица ссылается на _attachments,
		// поэтому заводится после неё. Блобы — второй источник публикации
		// (поле image), и без них матричный тест падает на «no such table:
		// _blobs» вместо внятного отказа (issue #827).
		{"attachments", db.EnsureAttachmentTable},
		{"blobs", db.EnsureBlobTable},
		{"public files", db.EnsurePublicFilesSchema},
		// Константы заводит MigrateConstants: таблица одна на все константы, и
		// пустой список создаёт её же.
		{"constants", func(ctx context.Context) error { return db.MigrateConstants(ctx, nil) }},
	}
	for _, s := range steps {
		if err := s.fn(ctx); err != nil {
			return fmt.Errorf("%s schema: %w", s.name, err)
		}
	}
	return nil
}
