// Package incident хранит последние ошибки и паники процесса и присваивает
// каждой короткий код вида E-3F7A2C.
//
// Смысл кода — превратить «у меня не работает» в адресуемый факт: код виден
// пользователю на экране ошибки, его можно продиктовать по телефону, и он же
// подставляется в отчёт «Сообщить об ошибке» вместе со стеком (план 115).
//
// Хранилище живёт в памяти и умирает вместе с процессом. Это осознанно: файл
// инцидентов стал бы ещё одним журналом со своим сроком хранения и своим
// вопросом про персональные данные, а задача здесь — «пользователь жалуется
// прямо сейчас».
package incident

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Виды инцидентов.
const (
	KindError = "error" // ошибка, отданная обработчиком как 500
	KindPanic = "panic" // паника, перехваченная Recoverer
)

// stackLimit — сколько байт стека сохраняем. Полный стек горутины бывает в
// сотни килобайт; для диагностики хватает верхушки, а в отчёт, который читает
// человек, больше и не влезет.
const stackLimit = 8 << 10

// DefaultLimit — сколько инцидентов держим в памяти.
const DefaultLimit = 50

// Record — один зарегистрированный инцидент.
type Record struct {
	ID    string    // "E-3F7A2C"
	Time  time.Time
	Kind  string    // KindError | KindPanic
	Where string    // "POST /ui/doc/заказ/new" — метод и путь, без строки запроса
	Text  string    // текст ошибки
	Stack string    // только для паник
	User  string    // логин: нужен, чтобы показать пользователю ЕГО инциденты; в отчёт не идёт
}

// Store — кольцевой буфер инцидентов. Устройство повторяет ui.MessageStore:
// мьютекс и срез с усечением с головы.
type Store struct {
	mu    sync.Mutex
	limit int
	list  []Record
}

func NewStore(limit int) *Store {
	if limit <= 0 {
		limit = DefaultLimit
	}
	return &Store{limit: limit}
}

// Record регистрирует инцидент и возвращает его с проставленными ID и временем.
// Возврат нужен вызывающему: код инцидента он показывает пользователю.
func (s *Store) Record(rec Record) Record {
	if rec.ID == "" {
		rec.ID = newID()
	}
	if rec.Time.IsZero() {
		rec.Time = time.Now()
	}
	if rec.Kind == "" {
		rec.Kind = KindError
	}
	if len(rec.Stack) > stackLimit {
		rec.Stack = rec.Stack[:stackLimit] + "\n… стек обрезан"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, rec)
	if len(s.list) > s.limit {
		s.list = s.list[len(s.list)-s.limit:]
	}
	return rec
}

// Recent возвращает последние n инцидентов пользователя, свежие первыми.
// Пустой user — все инциденты (так их видит администратор и лаунчер).
func (s *Store) Recent(user string, n int) []Record {
	if n <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, 0, n)
	for i := len(s.list) - 1; i >= 0 && len(out) < n; i-- {
		if user != "" && s.list[i].User != user {
			continue
		}
		out = append(out, s.list[i])
	}
	return out
}

// Get находит инцидент по коду. Регистр и пробелы не важны: код приходит из
// формы, куда пользователь переписал его с экрана руками.
func (s *Store) Get(id string) (Record, bool) {
	id = normalizeID(id)
	if id == "" {
		return Record{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.list) - 1; i >= 0; i-- {
		if s.list[i].ID == id {
			return s.list[i], true
		}
	}
	return Record{}, false
}

func normalizeID(id string) string {
	return strings.ToUpper(strings.TrimSpace(id))
}

// newID возвращает код инцидента. Шесть шестнадцатеричных знаков — 16 млн
// значений на полсотни живых записей: совпадение исключено, а продиктовать
// голосом всё ещё можно.
func newID() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Источник случайности недоступен — код всё равно нужен, иначе
		// пользователю не на что сослаться. Берём наносекунды.
		n := time.Now().UnixNano()
		b[0], b[1], b[2] = byte(n>>16), byte(n>>8), byte(n)
	}
	return "E-" + strings.ToUpper(hex.EncodeToString(b[:]))
}

// WhereOf описывает место инцидента: метод и путь запроса.
//
// Строка запроса отбрасывается целиком, а не маскируется по ключам: в фильтрах
// списков едут значения, введённые пользователем (фамилия, номер телефона), и
// они не имеют отношения к диагностике, зато поехали бы в отчёт.
func WhereOf(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return r.Method + " " + r.URL.Path
}
