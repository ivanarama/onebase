package metadata

import (
	"crypto/sha256"
	"encoding/hex"
	"unicode/utf8"
)

// MaxSQLIdentBytes — предел длины идентификатора PostgreSQL (NAMEDATALEN-1).
// Длиннее PostgreSQL молча обрезает имя, причём в байтах, а кириллица в UTF-8
// занимает два байта на букву: предел — около 31 буквы. Имена, пришедшие из 1С,
// часто длиннее, и после обрезки два разных реквизита становились одной
// колонкой, а таблица — своей же табличной частью (#1946). SQLite предела не
// имеет, но правило общее для обоих диалектов: одна конфигурация живёт на обоих,
// и универсальная выгрузка переносит таблицы между ними по имени.
const MaxSQLIdentBytes = 63

// sqlIdentHashHex — длина хвоста-отпечатка в hex-символах.
const sqlIdentHashHex = 8

// SQLIdent — физическое имя таблицы или колонки для логического имени name
// (уже в нижнем регистре, с префиксом/суффиксом платформы).
//
// Имя не длиннее MaxSQLIdentBytes возвращается как есть — поэтому все
// конфигурации с короткими именами не затронуты. Более длинное заменяется на
// «начало по границе символа» + "_" + первые 8 hex SHA-256 полного имени.
// Функция детерминирована и не зависит от диалекта: одно и то же имя даёт одно и
// то же физическое имя в любой базе.
func SQLIdent(name string) string {
	if len(name) <= MaxSQLIdentBytes {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	suffix := "_" + hex.EncodeToString(sum[:])[:sqlIdentHashHex]
	return clipUTF8(name, MaxSQLIdentBytes-len(suffix)) + suffix
}

// LegacySQLIdents — физические имена, под которыми длинное имя могло лежать в
// базе до введения SQLIdent: полное (SQLite хранит его как есть) и обрезанное
// PostgreSQL по границе символа. Для короткого имени — nil: переименовывать
// нечего. Нужна миграции, чтобы найти старую таблицу или колонку и переименовать
// её, а не завести рядом пустую.
func LegacySQLIdents(name string) []string {
	if len(name) <= MaxSQLIdentBytes {
		return nil
	}
	return []string{name, clipUTF8(name, MaxSQLIdentBytes)}
}

// clipUTF8 обрезает s до не более чем n байт, не разрывая символ, — как
// pg_mbcliplen при обрезке идентификатора PostgreSQL.
func clipUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
