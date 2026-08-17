// relsign — генерация ключей и подпись файлов контрольных сумм релиза (#783).
//
// Две команды, обе нужны в разных местах:
//
//	relsign keygen                       — один раз, на машине владельца
//	relsign sign -key <base64> file…     — в релизном workflow
//
// Подписывается файл .sha256, а не архив: подпись тянут перед закачкой
// многомегабайтного архива, и цепочка получается полной — подпись подтверждает
// сумму, сумма подтверждает архив.
//
// Приватный ключ принимается ТОЛЬКО из переменной окружения или флага. Файла с
// ключом нет намеренно: в CI он приходит секретом, а на машине владельца лежит
// там, где владелец решил, и утилита не должна знать это место.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen()
	case "sign":
		err = sign(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "relsign:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `relsign — ключи и подписи релиза onebase

  relsign keygen
      Печатает новую пару ключей. Приватный положить в секрет репозитория
      (RELEASE_SIGNING_KEY), открытый — в переменную сборки RELEASE_PUBLIC_KEY.

  relsign sign -key <base64> [-pub <base64>] файл…
      Подписывает каждый файл, рядом кладёт <файл>.sig. Ключи можно передать
      переменными окружения RELEASE_SIGNING_KEY и RELEASE_PUBLIC_KEY вместо
      флагов. Открытый ключ нужен не для подписи, а для сверки половин пары:
      без него рассинхрон при ротации даёт зелёный релиз, который не примет
      ни один установленный бинарь.
`)
}

func keygen() error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	fmt.Println("# приватный ключ — секрет репозитория RELEASE_SIGNING_KEY")
	fmt.Println("# он НЕ должен попасть в git, в журналы CI и в issue")
	fmt.Println(base64.StdEncoding.EncodeToString(priv))
	fmt.Println()
	fmt.Println("# открытый ключ — переменная RELEASE_PUBLIC_KEY, вшивается в бинарь")
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	keyFlag := fs.String("key", "", "приватный ключ base64 (иначе RELEASE_SIGNING_KEY)")
	pubFlag := fs.String("pub", "", "ожидаемый открытый ключ base64 (иначе RELEASE_PUBLIC_KEY): сверка половин пары")
	if err := fs.Parse(args); err != nil {
		return err
	}
	key := *keyFlag
	if strings.TrimSpace(key) == "" {
		key = os.Getenv("RELEASE_SIGNING_KEY")
	}
	priv, err := parsePrivateKey(key)
	if err != nil {
		return err
	}
	expectedPub := *pubFlag
	if strings.TrimSpace(expectedPub) == "" {
		expectedPub = os.Getenv("RELEASE_PUBLIC_KEY")
	}
	if err := checkKeyPair(priv, expectedPub); err != nil {
		return err
	}
	files := fs.Args()
	if len(files) == 0 {
		return errors.New("нечего подписывать: укажите файлы")
	}
	for _, path := range files {
		if err := signFile(priv, path); err != nil {
			return err
		}
	}
	return nil
}

func signFile(priv ed25519.PrivateKey, path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // G304: путь приходит из аргументов релизного workflow
	if err != nil {
		return err
	}
	sig := ed25519.Sign(priv, data)
	out := path + ".sig"
	// 0644: подпись публична по определению — её проверяют все, кто обновляется.
	if err := os.WriteFile(out, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil { //nolint:gosec // G306: подпись — публичный артефакт релиза
		return err
	}
	fmt.Println("подписан", filepath.Base(path), "→", filepath.Base(out))
	return nil
}

// checkKeyPair сверяет приватный ключ с ожидаемым открытым (#967).
//
// Половинки пары живут в разных местах: приватный в секретах, открытый в
// переменных, и связывает их только аккуратность того, кто заводил. Если
// перепутать при ротации, релиз выйдет зелёным — суммы посчитаны, подписи
// наложены, ассеты на месте, — но принять его не сможет ни один установленный
// бинарь: несовпадение подписи отвергается всегда, в любом режиме. Узнать об
// этом можно было бы только от пользователей.
//
// Пустой ожидаемый ключ — не ошибка: форк подписывает своим ключом и переменную
// платформы не заводит. Сверять тогда не с чем, и требовать её значило бы
// сломать форкам релиз ради проверки, которая им не нужна.
func checkKeyPair(priv ed25519.PrivateKey, expectedPub string) error {
	expectedPub = strings.TrimSpace(expectedPub)
	if expectedPub == "" {
		fmt.Fprintln(os.Stderr, "relsign: открытый ключ для сверки не задан (-pub или RELEASE_PUBLIC_KEY) — "+
			"подписываю без проверки пары")
		return nil
	}
	want, err := base64.StdEncoding.DecodeString(expectedPub)
	if err != nil {
		return fmt.Errorf("открытый ключ для сверки не читается: %w", err)
	}
	if len(want) != ed25519.PublicKeySize {
		return fmt.Errorf("открытый ключ для сверки должен быть %d байт, получено %d",
			ed25519.PublicKeySize, len(want))
	}
	got, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return errors.New("приватный ключ не отдаёт открытую половину")
	}
	if !got.Equal(ed25519.PublicKey(want)) {
		return fmt.Errorf("половины ключа не сходятся: приватный ключ соответствует открытому %s, "+
			"а вшивается в сборку %s. Такой релиз выйдет зелёным, но его не примет ни один "+
			"установленный бинарь — сверьте RELEASE_SIGNING_KEY и RELEASE_PUBLIC_KEY",
			base64.StdEncoding.EncodeToString(got), expectedPub)
	}
	return nil
}

func parsePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("приватный ключ не читается: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("приватный ключ должен быть %d байт, получено %d "+
			"(ожидается вывод `relsign keygen`)", ed25519.PrivateKeySize, len(raw))
	}
	return ed25519.PrivateKey(raw), nil
}
