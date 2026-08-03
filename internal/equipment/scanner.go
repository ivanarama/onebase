package equipment

import (
	"bufio"
	"context"
	"fmt"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"strings"
)

func init() {
	Register("scanner_tcp", func() Device { return &scannerDevice{} })
}

// scannerDevice — драйвер сетевого сканера штрих-кодов: устройство шлёт коды
// асинхронно (по строке на скан). В отличие от остальных драйверов это не
// запрос-ответ, а поток событий «внутрь» — отсюда интерфейс EventSource.
//
// (HID-сканеры, эмулирующие клавиатуру, драйвера не требуют — ввод приходит
// прямо в поле формы; этот драйвер — для сетевых/потоковых сканеров.)
type scannerDevice struct {
	conn rwTransport
}

func (d *scannerDevice) Kind() string { return "сканер" }

func (d *scannerDevice) Connect(params map[string]string) error {
	conn, err := openRWTransport(params)
	if err != nil {
		return err
	}
	d.conn = conn
	return nil
}

func (d *scannerDevice) Disconnect() error {
	if d.conn == nil {
		return nil
	}
	err := d.conn.Close()
	d.conn = nil
	return err
}

// Stream читает штрихкоды построчно и вызывает fn на каждый непустой код, пока
// не закроют соединение (EOF) или не отменят контекст. Отмена контекста
// (разрыв клиента SSE) закрывает соединение и прерывает чтение.
func (d *scannerDevice) Stream(ctx context.Context, fn func(string)) error {
	if d.conn == nil {
		return fmt.Errorf("устройство не подключено")
	}
	conn := d.conn // захватываем: Disconnect может обнулить d.conn параллельно
	// Производный контекст с cancel: при выходе из Stream по EOF сканера (а не по
	// отмене внешнего ctx) defer cancel() разблокирует горутину-«сторожа», иначе
	// при неотменяемом ctx (context.Background) она зависла бы навсегда на ctx.Done().
	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx2.Done()
		oblog.CloseQuiet("equipment", "соединение со сканером", conn)
	}()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		if code := strings.TrimSpace(sc.Text()); code != "" {
			fn(code)
		}
	}
	// Различаем штатную отмену ИЗВНЕ от выхода по EOF: ctx.Err() (не ctx2) ≠ nil
	// только если отменили внешний контекст или разорвали клиента SSE.
	if ctx.Err() != nil {
		return nil // штатное завершение по отмене/разрыву клиента
	}
	return sc.Err()
}
