package equipment

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"go.bug.st/serial"
)

// openWriteTransport открывает транспорт «только на запись» для устройств,
// которые принимают команды без ответа (принтер чеков, дисплей покупателя).
// Транспорт выбирается по параметру "Порт":
//
//	"192.168.1.50:9100" → TCP (содержит host:port)
//	"COM3" | "/dev/ttyUSB0" → serial (скорость из "Скорость"/"baud", по умолч. 9600)
//
// Так один драйвер работает и по сети, и по COM — имя драйвера описывает протокол
// устройства (escpos/display), а не транспорт.
func openWriteTransport(params map[string]string) (io.WriteCloser, error) {
	addr := firstNonEmpty(params["порт"], params["port"])
	if addr == "" {
		return nil, fmt.Errorf("не указан параметр \"Порт\" (например, 192.168.1.50:9100 или COM3)")
	}
	if isSerialAddr(addr) {
		return openSerial(addr, params)
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// isSerialAddr отличает serial-порт (COM3, /dev/ttyUSB0) от сетевого host:port.
func isSerialAddr(addr string) bool {
	return !strings.Contains(addr, ":")
}

func openSerial(addr string, params map[string]string) (io.WriteCloser, error) {
	return openSerialPort(addr, params)
}

func openSerialPort(addr string, params map[string]string) (serial.Port, error) {
	baud := 9600
	if b := firstNonEmpty(params["скорость"], params["baud"]); b != "" {
		if n, err := strconv.Atoi(b); err == nil && n > 0 {
			baud = n
		}
	}
	port, err := serial.Open(addr, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, fmt.Errorf("serial %s: %w", addr, err)
	}
	return port, nil
}

// ─── транспорт чтения-записи (устройства «запрос-ответ») ─────────────────────

// rwTransport — транспорт с чтением, записью и таймаутом чтения, общий для
// устройств «запрос-ответ» (весы, эквайринг, scripted, сканер). Скрывает
// разницу net.Conn.SetReadDeadline и serial.Port.SetReadTimeout, так что эти
// драйверы тоже работают и по TCP, и по serial.
type rwTransport interface {
	io.ReadWriteCloser
	SetReadTimeout(d time.Duration) error
}

type tcpTransport struct{ net.Conn }

func (t tcpTransport) SetReadTimeout(d time.Duration) error {
	return t.SetReadDeadline(time.Now().Add(d))
}

// serialTransport: serial.Port уже реализует Read/Write/Close и SetReadTimeout,
// то есть удовлетворяет rwTransport как есть.
type serialTransport struct{ serial.Port }

// readFrame дочитывает ответ устройства до полного фрейма: один conn.Read для
// TCP/serial не гарантирует, что пришёл весь ответ (фрейм может прийти кусками),
// поэтому накапливаем буфер, пока не встретим терминатор (CR '\r' или ETX 0x03)
// либо пока чтение не завершится (EOF/тишина по read-timeout). Иначе обрезанный
// ответ даёт ложный результат разбора — критично для эквайринга, где это
// означало бы Approved=false уже после реального списания.
//
// Команды драйверов шлются с терминатором '\r', поэтому он и служит признаком
// конца ответа; для устройств без терминатора (некоторые весы) фрейм собирается
// до короткой паузы/EOF, после чего возвращается накопленное.
func readFrame(r io.Reader) ([]byte, error) {
	var out []byte
	chunk := make([]byte, 256)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			fresh := chunk[:n]
			out = append(out, fresh...)
			// Терминатор фрейма получен — ответ полон, дочитывать не нужно.
			if bytes.IndexByte(fresh, '\r') >= 0 || bytes.IndexByte(fresh, 0x03) >= 0 {
				return out, nil
			}
		}
		if err != nil {
			// Тишина по read-timeout или EOF: то, что накоплено, и есть ответ.
			// Ошибку отдаём только если не получили вообще ничего.
			if len(out) > 0 {
				return out, nil
			}
			return nil, err
		}
	}
}

// openRWTransport открывает read-write транспорт по параметру "Порт":
// TCP (host:port) или serial (COM3, /dev/tty*).
func openRWTransport(params map[string]string) (rwTransport, error) {
	addr := firstNonEmpty(params["порт"], params["port"])
	if addr == "" {
		return nil, fmt.Errorf("не указан параметр \"Порт\" (например, 192.168.1.52:9100 или COM3)")
	}
	if isSerialAddr(addr) {
		port, err := openSerialPort(addr, params)
		if err != nil {
			return nil, err
		}
		return serialTransport{port}, nil
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return tcpTransport{conn}, nil
}
