package search

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"sync"
)

// Пагинация поиска наружу не отдаёт число.
//
// Смещение в индексе считается по ПРОСМОТРЕННЫМ строкам, то есть до отсева
// маскированием (план 88) и строковыми политиками (план 79). Отданное клиенту,
// оно превращает поиск в оракул: разница «просмотрено» и «показано» сообщает,
// что совпадение было, хотя видеть его нельзя. Так побайтово восстанавливается
// скрытый телефон или ИНН — по 10 запросов на знак, — и в журнал раскрытия при
// этом ничего не пишется.
//
// Поэтому позиция чтения уезжает клиенту только внутри курсора: AES-GCM со
// случайным одноразовым номером. Одно и то же смещение каждый раз даёт разный
// текст, поэтому сравнивать курсоры между запросами бесполезно; подобрать
// чужую позицию тоже нельзя — тег GCM отбрасывает подделку. Числовое смещение
// снаружи не принимается вовсе: иначе перебором позиций получился бы тот же
// оракул с другой стороны.
//
// Ключ живёт в процессе и переживает только его: курсор — состояние листания,
// а не ссылка, которую сохраняют в закладки. После перезапуска старый курсор
// не расшифруется, и листание начнётся сначала — см. DecodeCursor.

var (
	cursorKeyOnce sync.Once
	cursorAEAD    cipher.AEAD
)

func cursorCipher() cipher.AEAD {
	cursorKeyOnce.Do(func() {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			// crypto/rand не отдал байты — процессу дальше делать нечего,
			// но поиск не должен ронять сервер: без шифра курсоров не будет,
			// а листание выродится в одну страницу (см. EncodeCursor).
			return
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return
		}
		cursorAEAD = aead
	})
	return cursorAEAD
}

// EncodeCursor упаковывает позицию чтения в непрозрачный курсор. Пустая строка —
// продолжения нет (или шифр недоступен).
func EncodeCursor(offset int) string {
	aead := cursorCipher()
	if aead == nil || offset <= 0 {
		return ""
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	var plain [8]byte
	binary.BigEndian.PutUint64(plain[:], uint64(offset))
	sealed := aead.Seal(nonce, nonce, plain[:], nil)
	return base64.RawURLEncoding.EncodeToString(sealed)
}

// DecodeCursor разбирает курсор обратно в позицию чтения. Непригодный курсор
// (подделка, курсор от прошлого запуска процесса) — не ошибка: листание просто
// начинается сначала, как при первом запросе.
func DecodeCursor(s string) int {
	if s == "" {
		return 0
	}
	aead := cursorCipher()
	if aead == nil {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) < aead.NonceSize()+8 {
		return 0
	}
	nonce := raw[:aead.NonceSize()]
	plain, err := aead.Open(nil, nonce, raw[aead.NonceSize():], nil)
	if err != nil || len(plain) != 8 {
		return 0
	}
	n := binary.BigEndian.Uint64(plain)
	if n > 1<<31 {
		return 0
	}
	return int(n)
}
