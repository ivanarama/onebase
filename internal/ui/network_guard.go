package ui

// Предохранитель сети (план 62): единая проверка для всех инициируемых
// конфигурацией сетевых операций — HTTP-клиент/email в DSL, исходящие веб-хуки
// (план 29), входящие HTTP-сервисы (план 61). Флаг net.enabled читается из
// _settings (см. storage), поэтому переключение в конфигураторе действует без
// перезапуска сервера.

import (
	"context"
	"errors"

	"github.com/ivantit66/onebase/internal/storage"
)

// ErrNetworkLocked — текст отказа, видимый пользователю (DSL-ошибка, тело
// ответа сервиса, запись в журнале хуков). Прямо подсказывает, где включить.
// Текст называет и путь в конфигураторе, и ключ настройки с командой: в
// headless-окружении (CI, автопрогон на свежей базе) до конфигуратора не дойти,
// и одного пункта меню там мало (#709). Сам путь — в storage.NetworkEnabledHint,
// одной строкой на все отказы: пока копий было три, они разъехались с меню.
var ErrNetworkLocked = errors.New("сетевые возможности отключены предохранителем — " + storage.NetworkEnabledHint)

// netEnabled сообщает, разрешены ли сетевые операции для текущей базы.
func (s *Server) netEnabled(ctx context.Context) bool {
	return s.store.GetNetworkEnabled(ctx)
}

// netGuard возвращает замыкание-страж для DSL (HTTP/email builtins): nil-ошибка
// при разрешённой сети, ErrNetworkLocked при заблокированной.
func (s *Server) netGuard(ctx context.Context) func() error {
	return func() error {
		if s.netEnabled(ctx) {
			return nil
		}
		return ErrNetworkLocked
	}
}
