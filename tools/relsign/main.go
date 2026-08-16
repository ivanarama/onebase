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

  relsign sign -key <base64> файл…
      Подписывает каждый файл, рядом кладёт <файл>.sig. Ключ можно передать
      переменной окружения RELEASE_SIGNING_KEY вместо флага.
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
