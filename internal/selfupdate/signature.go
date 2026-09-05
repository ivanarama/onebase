package selfupdate

// Подпись релиза (#783, план 92).
//
// До неё доверие к обновлению равнялось доверию к аккаунту GitHub: контрольная
// сумма лежит там же, где архив, и тот, кто получил доступ к репозиторию,
// подменяет обе. Подпись эту связь разрывает — подделать её без приватного
// ключа нельзя даже с полным доступом к релизам.
//
// Подписывается файл контрольной суммы, а не архив. Так подпись остаётся
// маленькой (её тянут до многомегабайтной закачки), а цепочка получается
// полной: подпись подтверждает сумму, сумма подтверждает архив.

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// PublicKey — открытый ключ подписи, base64 (std). ПОДСТАВЛЯЕТСЯ ПРИ СБОРКЕ:
//
//	go build -ldflags "-X github.com/ivantit66/onebase/internal/selfupdate.PublicKey=<base64>"
//
// Не константа в исходниках намеренно. Форк, собравший платформу из исходников,
// подписывает свои релизы СВОИМ ключом и вшивает свой открытый — иначе его
// собственное обновление отвергало бы его же сборки. Пустой ключ означает
// «проверка выключена»: `go build ./cmd/onebase` из клона работает ровно как
// раньше, и контрибьютору не нужно знать про ключи вовсе.
var PublicKey string

// RequireSignature — жёсткий режим: релиз без подписи отвергается.
// Подставляется при сборке тем же способом (значение "1"/"true").
//
// Официальные релизы собираются с ним начиная с #1195: релизный workflow
// вшивает флаг рядом с открытым ключом, и это проверяется тестом
// release_workflow_test.go. Мягким переход был по решению владельца — сначала
// релизы подписываются, а обновление подпись ПРОВЕРЯЕТ, но не ТРЕБУЕТ, — иначе
// первая же подписанная версия оборвала бы автообновление всем, кто стоял на
// сборках без подписи. Своё дело мягкость сделала и была снята: обойти подпись
// можно было именно её отсутствием.
//
// Пустое значение (мягкий режим) остаётся рабочим состоянием для сборки из
// исходников и для форка, который вводит подпись у себя и повторяет тот же
// переход.
var RequireSignature string

// ErrSignatureInvalid — подпись есть, но не сходится. Это отказ, а не
// предупреждение: расхождение означает либо подмену, либо чужой ключ.
var ErrSignatureInvalid = errors.New("selfupdate: подпись релиза не совпадает")

// ErrSignatureMissing — подписи нет там, где она обязательна.
var ErrSignatureMissing = errors.New("selfupdate: релиз не подписан")

// SignatureEnforced сообщает, требует ли эта сборка подпись обязательно.
func SignatureEnforced() bool {
	switch strings.ToLower(strings.TrimSpace(RequireSignature)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// SignatureConfigured сообщает, вшит ли в эту сборку открытый ключ. Без ключа
// проверять нечем, и обновление ведёт себя как до #783.
func SignatureConfigured() bool { return strings.TrimSpace(PublicKey) != "" }

// VerifySignature проверяет подпись содержимого файла контрольной суммы.
//
// signature — то, что лежит в ассете <архив>.sha256.sig: base64 (std) от 64
// байт Ed25519. Разрешаем перевод строки в конце — файл создаётся утилитой и
// может уехать через редактор.
func VerifySignature(shaFileContent, signature []byte) error {
	key, err := parsePublicKey(PublicKey)
	if err != nil {
		return err
	}
	sig, err := decodeSignature(signature)
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, shaFileContent, sig) {
		return ErrSignatureInvalid
	}
	return nil
}

func parsePublicKey(encoded string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("selfupdate: открытый ключ подписи не читается: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("selfupdate: открытый ключ подписи должен быть %d байт, получено %d",
			ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

func decodeSignature(signature []byte) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signature)))
	if err != nil {
		return nil, fmt.Errorf("selfupdate: подпись не читается: %w", err)
	}
	if len(raw) != ed25519.SignatureSize {
		return nil, fmt.Errorf("selfupdate: подпись должна быть %d байт, получено %d",
			ed25519.SignatureSize, len(raw))
	}
	return raw, nil
}
