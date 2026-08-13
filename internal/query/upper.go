package query

import "strings"

// Смена регистра для сопоставления идентификаторов языка запросов.
//
// strings.ToUpper/ToLower дёшевы только на чистом ASCII: там у них отдельный
// быстрый путь. Первый же байт ≥ 0x80 отправляет строку в strings.Map — это
// подекодная развёртка UTF-8, вызов unicode.ToUpper с двоичным поиском по
// таблицам диапазонов и сборка результата через strings.Builder. Язык запросов
// кириллический, поэтому туда попадает КАЖДЫЙ идентификатор: в профиле
// компиляции реального отчёта strings.ToUpper занимал 49% CPU (из них 56% —
// sqlKW), а strings.ToLower — ещё 25%.
//
// Здесь тот же результат получается побайтово, без декодирования рун и без
// таблиц: латиница и кириллица (U+0400..U+045F, где живёт и ё) переводятся
// арифметикой над двумя байтами UTF-8. Всё остальное — диакритика, греческий,
// архаичная кириллица, битый UTF-8 — уходит в стандартную функцию: подменять
// общий Unicode своей таблицей нельзя.
//
// Два свойства, на которых всё держится:
//   - длина в байтах не меняется: строчная и заглавная кириллические буквы
//     кодируются двумя байтами. Отсюда отсечение по длине в upperLookup и
//     запись в буфер на стеке;
//   - если менять нечего, возвращается ИСХОДНАЯ строка. Так делает strings.Map,
//     и без этого замена вышла бы дороже по аллокациям, чем стандартная функция.

// caseStep разбирает символ, начинающийся с s[i], и возвращает его в нужном
// регистре: n — длина в байтах (1 или 2), b0/b1 — байты результата. ok=false
// означает, что символ вне быстрого пути и строку надо отдать в стандартную
// функцию целиком.
func caseStep(s string, i int, toUpper bool) (b0, b1 byte, n int, ok bool) {
	b := s[i]
	if b < 0x80 {
		if toUpper {
			if b >= 'a' && b <= 'z' {
				b -= 'a' - 'A'
			}
		} else if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		return b, 0, 1, true
	}
	if b != 0xD0 && b != 0xD1 || i+1 >= len(s) {
		return 0, 0, 0, false
	}
	lead, trail := b, s[i+1]
	if toUpper {
		switch {
		case lead == 0xD0 && trail >= 0x80 && trail <= 0xAF:
			// Ѐ..Я — уже заглавные.
		case lead == 0xD0 && trail >= 0xB0 && trail <= 0xBF:
			trail -= 0x20 // а..п → А..П
		case lead == 0xD1 && trail >= 0x80 && trail <= 0x8F:
			lead, trail = 0xD0, trail+0x20 // р..я → Р..Я
		case lead == 0xD1 && trail >= 0x90 && trail <= 0x9F:
			lead, trail = 0xD0, trail-0x10 // ѐ..џ → Ѐ..Џ, сюда же ё → Ё
		default:
			return 0, 0, 0, false
		}
		return lead, trail, 2, true
	}
	switch {
	case lead == 0xD0 && trail >= 0x80 && trail <= 0x8F:
		lead, trail = 0xD1, trail+0x10 // Ѐ..Џ → ѐ..џ, сюда же Ё → ё
	case lead == 0xD0 && trail >= 0x90 && trail <= 0x9F:
		trail += 0x20 // А..П → а..п
	case lead == 0xD0 && trail >= 0xA0 && trail <= 0xAF:
		lead, trail = 0xD1, trail-0x20 // Р..Я → р..я
	case lead == 0xD0 && trail >= 0xB0 && trail <= 0xBF:
		// а..п — уже строчные.
	case lead == 0xD1 && trail >= 0x80 && trail <= 0x9F:
		// р..я, ѐ..џ — уже строчные.
	default:
		return 0, 0, 0, false
	}
	return lead, trail, 2, true
}

// appendCase дописывает в dst преобразованную s. ok=false — строка вне
// быстрого пути, содержимое dst использовать нельзя.
func appendCase(dst []byte, s string, toUpper bool) ([]byte, bool) {
	for i := 0; i < len(s); {
		b0, b1, n, ok := caseStep(s, i, toUpper)
		if !ok {
			return nil, false
		}
		if n == 1 {
			dst = append(dst, b0)
		} else {
			dst = append(dst, b0, b1)
		}
		i += n
	}
	return dst, true
}

// caseFast — замена strings.ToUpper/ToLower для текста запроса.
func caseFast(s string, toUpper bool) string {
	// Первый проход только ищет, есть ли что менять: у большинства строк
	// результат совпадает с исходной, и тогда аллокации нет вовсе.
	for i := 0; i < len(s); {
		b0, b1, n, ok := caseStep(s, i, toUpper)
		if !ok {
			return stdCase(s, toUpper)
		}
		if b0 != s[i] || n == 2 && b1 != s[i+1] {
			return caseFastFrom(s, i, toUpper)
		}
		i += n
	}
	return s
}

// caseFastFrom строит результат, зная, что до from менять нечего. Одна
// аллокация: Builder.Grow выделяет буфер, а String отдаёт его без копии.
func caseFastFrom(s string, from int, toUpper bool) string {
	var sb strings.Builder
	sb.Grow(len(s))
	sb.WriteString(s[:from])
	for i := from; i < len(s); {
		b0, b1, n, ok := caseStep(s, i, toUpper)
		if !ok {
			return stdCase(s, toUpper)
		}
		sb.WriteByte(b0)
		if n == 2 {
			sb.WriteByte(b1)
		}
		i += n
	}
	return sb.String()
}

func stdCase(s string, toUpper bool) string {
	if toUpper {
		return strings.ToUpper(s)
	}
	return strings.ToLower(s)
}

func upperFast(s string) string { return caseFast(s, true) }

func lowerFast(s string) string { return caseFast(s, false) }

// maxLookupKeyBytes — длина самого длинного ключа в картах ключевых слов.
// В быстром пути апперкейс длину не меняет, поэтому строка длиннее заведомо не
// ключевое слово: отсечь можно до всякой работы.
var maxLookupKeyBytes = func() int {
	longest := 0
	for _, m := range []map[string]string{kwMap, aggFuncs} {
		for key := range m {
			if len(key) > longest {
				longest = len(key)
			}
		}
	}
	return longest
}()

// upperLookup ищет ident в m без единой аллокации: апперкейс пишется в буфер на
// стеке, а конструкция m[string(байты)] распознаётся компилятором и не
// материализует ключ в куче. Вызывается на каждый идентификатор запроса, а
// запрос компилируется на каждое Выполнить() — поэтому и без аллокаций.
func upperLookup(m map[string]string, ident string) (string, bool) {
	if len(ident) > maxLookupKeyBytes {
		// В быстром ASCII/кириллическом пути регистр не меняет длину, поэтому
		// слишком длинное имя действительно не может стать ключевым словом.
		// Но общий Unicode может сжиматься: например, трёхбайтовая `ᲄ`
		// (U+1C84) в верхнем регистре становится двухбайтовой `Т`. Такой ввод
		// обязан сохранить прежнюю семантику strings.ToUpper и пройти fallback.
		for i := 0; i < len(ident); {
			_, _, n, ok := caseStep(ident, i, true)
			if !ok {
				value, found := m[strings.ToUpper(ident)]
				return value, found
			}
			i += n
		}
		return "", false
	}
	var buf [maxInlineKeyBytes]byte
	if upper, ok := appendCase(buf[:0], ident, true); ok {
		value, found := m[string(upper)]
		return value, found
	}
	value, found := m[strings.ToUpper(ident)]
	return value, found
}

// maxInlineKeyBytes с запасом покрывает самое длинное ключевое слово
// («СГРУППИРОВАТЬ» — 26 байт). Если ключ окажется длиннее, append просто
// возьмёт кучу: медленнее, но по-прежнему верно.
const maxInlineKeyBytes = 64
