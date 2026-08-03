package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Откуда берётся мастер-ключ. Файл имеет приоритет над переменной: в
// docker/k8s секрет монтируют файлом, и это надёжнее, чем переменная окружения,
// которая видна в /proc и в выводе инспекции контейнера.
const (
	EnvMasterKey     = "ONEBASE_MASTER_KEY"
	EnvMasterKeyFile = "ONEBASE_MASTER_KEY_FILE"
)

// Формат enc:-значения, версия 1:
//
//	[1 байт версии][4 байта отпечатка ключа][12 байт nonce][шифротекст+тег GCM]
//
// Отпечаток ключа хранится открыто и намеренно: он не раскрывает ключ (первые
// четыре байта SHA-256), зато позволяет сказать «зашифровано другим ключом»
// вместо невнятного «расшифровка не удалась» и даёт rotate понять, какие
// значения уже перешифрованы, а какие нет.
//
// Байт версии оставлен на смену алгоритма: старые значения тогда останутся
// читаемыми.
const (
	encVersion = 1
	keyIDLen   = 4
	nonceLen   = 12
	keyLen     = 32 // AES-256
)

// Параметры вывода ключа из парольной фразы. Соль фиксированная — иначе один и
// тот же пароль давал бы разные ключи на разных машинах, и база, снятая с
// одного сервера, не расшифровалась бы на другом. Фиксированная соль ослабляет
// защиту от предвычислений, поэтому парольная фраза — запасной вариант:
// штатный путь — 32-байтный ключ от `onebase secret keygen`.
const pbkdf2Iter = 600_000

var pbkdf2Salt = []byte("onebase-secrets-v1")

// Ошибки разыменования enc:.
var (
	// ErrNoMasterKey — enc:-значение есть, а ключа нет. Подсистема, которой
	// нужен этот секрет, обязана выключиться (fail-closed), а не работать
	// с пустым значением.
	ErrNoMasterKey = errors.New("secrets: мастер-ключ не задан (" + EnvMasterKey + " или " + EnvMasterKeyFile + ")")
	// ErrWrongKey — значение зашифровано другим ключом (частый случай после
	// восстановления базы на сервере с иным мастер-ключом).
	ErrWrongKey = errors.New("secrets: значение зашифровано другим мастер-ключом")
	// ErrNotEncrypted — значение не является enc:-ссылкой.
	ErrNotEncrypted = errors.New("secrets: значение не является enc:-ссылкой")
)

// Key — мастер-ключ шифрования секретов.
type Key struct {
	raw  []byte
	id   []byte
	aead cipher.AEAD
}

// GenerateKey создаёт новый случайный ключ (onebase secret keygen).
func GenerateKey() (*Key, error) {
	b := make([]byte, keyLen)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("secrets: генерация ключа: %w", err)
	}
	return newKey(b)
}

func newKey(raw []byte) (*Key, error) {
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("secrets: инициализация шифра: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: инициализация GCM: %w", err)
	}
	sum := sha256.Sum256(raw)
	return &Key{raw: raw, id: sum[:keyIDLen], aead: aead}, nil
}

// ParseKey разбирает значение мастер-ключа. Принимаются:
//
//	64 hex-знака       — то, что печатает `onebase secret keygen`;
//	base64 от 32 байт  — тот же ключ в другой записи;
//	любая другая строка — парольная фраза, из которой ключ выводится PBKDF2.
//
// Парольная фраза поддержана потому, что администратор всё равно её выберет;
// лучше вывести из неё ключ правильно, чем получить обрезанный до 32 байт пароль.
func ParseKey(v string) (*Key, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, ErrNoMasterKey
	}
	if b, err := hex.DecodeString(v); err == nil && len(b) == keyLen {
		return newKey(b)
	}
	if b, err := base64.StdEncoding.DecodeString(v); err == nil && len(b) == keyLen {
		return newKey(b)
	}
	dk, err := pbkdf2.Key(sha256.New, v, pbkdf2Salt, pbkdf2Iter, keyLen)
	if err != nil {
		return nil, fmt.Errorf("secrets: вывод ключа из парольной фразы: %w", err)
	}
	return newKey(dk)
}

// LoadKey читает мастер-ключ из окружения. Ключ не задан → (nil, nil): база
// может не пользоваться enc:-значениями, и это нормальный режим работы.
func LoadKey(getenv func(string) string, readFile func(string) ([]byte, error)) (*Key, error) {
	if p := strings.TrimSpace(getenv(EnvMasterKeyFile)); p != "" {
		b, err := readFile(p)
		if err != nil {
			return nil, fmt.Errorf("secrets: чтение %s=%s: %w", EnvMasterKeyFile, p, err)
		}
		return ParseKey(string(b))
	}
	if v := strings.TrimSpace(getenv(EnvMasterKey)); v != "" {
		return ParseKey(v)
	}
	return nil, nil
}

// Hex возвращает ключ в виде, пригодном для ONEBASE_MASTER_KEY.
func (k *Key) Hex() string { return hex.EncodeToString(k.raw) }

// ID — открытый отпечаток ключа (8 hex-знаков): им помечены enc:-значения.
func (k *Key) ID() string { return hex.EncodeToString(k.id) }

// Encrypt шифрует значение и возвращает готовую enc:-ссылку.
func (k *Key) Encrypt(plain string) (string, error) {
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secrets: генерация nonce: %w", err)
	}
	env := make([]byte, 0, 1+keyIDLen+nonceLen+len(plain)+k.aead.Overhead())
	env = append(env, encVersion)
	env = append(env, k.id...)
	env = append(env, nonce...)
	env = k.aead.Seal(env, nonce, []byte(plain), nil)
	return SchemeEnc + ":" + base64.StdEncoding.EncodeToString(env), nil
}

// Decrypt разворачивает enc:-ссылку. Принимает значение как с префиксом enc:,
// так и без него.
func (k *Key) Decrypt(ref string) (string, error) {
	raw, err := decodeRef(ref)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(raw[1:1+keyIDLen], k.id) {
		return "", ErrWrongKey
	}
	nonce := raw[1+keyIDLen : 1+keyIDLen+nonceLen]
	out, err := k.aead.Open(nil, nonce, raw[1+keyIDLen+nonceLen:], nil)
	if err != nil {
		// Отпечаток совпал, а тег нет: значение испорчено при переносе
		// (обрезано, перекодировано) — ключ здесь ни при чём.
		return "", fmt.Errorf("secrets: значение повреждено или изменено: %w", err)
	}
	return string(out), nil
}

// RefKeyID возвращает отпечаток ключа, которым зашифровано значение. Нужен
// диагностике (`onebase secret list`) и ротации, чтобы не перешифровывать
// дважды и внятно объяснять «этим ключом не открывается».
func RefKeyID(ref string) (string, bool) {
	raw, err := decodeRef(ref)
	if err != nil {
		return "", false
	}
	return hex.EncodeToString(raw[1 : 1+keyIDLen]), true
}

func decodeRef(ref string) ([]byte, error) {
	payload := strings.TrimSpace(ref)
	if scheme, arg, ok := splitBare(payload); ok {
		if scheme != SchemeEnc {
			return nil, ErrNotEncrypted
		}
		payload = strings.TrimSpace(arg)
	} else if m := refPattern.FindStringSubmatch(payload); m != nil && m[0] == payload && m[1] == SchemeEnc {
		payload = strings.TrimSpace(m[2])
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("secrets: enc:-значение не разбирается как base64: %w", err)
	}
	if len(raw) < 1+keyIDLen+nonceLen+1 {
		return nil, errors.New("secrets: enc:-значение короче заголовка")
	}
	if raw[0] != encVersion {
		return nil, fmt.Errorf("secrets: неизвестная версия формата enc: %d", raw[0])
	}
	return raw, nil
}
