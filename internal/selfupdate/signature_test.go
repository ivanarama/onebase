package selfupdate

// Подпись релиза (#783) проверяется через ПУБЛИЧНЫЙ путь — Download, — а не
// вызовом VerifySignature напрямую: защита имеет смысл ровно в той мере, в
// какой она стоит на пути обновления. Повод — #611: зелёный тест на функции,
// которую боевой путь не зовёт, хуже отсутствующего.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// signedReleaseServer отдаёт архив, контрольную сумму и — если sign=true —
// подпись этой суммы, как это делает GitHub после релизного workflow.
func signedReleaseServer(t *testing.T, body []byte, shaBody string, sig []byte) (*httptest.Server, Release) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/asset.zip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/asset.zip.sha256", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(shaBody))
	})
	rel := Release{
		Tag:       "build-900",
		AssetName: "asset.zip",
		SHAURL:    "",
	}
	if sig != nil {
		mux.HandleFunc("/asset.zip.sha256.sig", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(sig)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	rel.AssetURL = srv.URL + "/asset.zip"
	rel.SHAURL = srv.URL + "/asset.zip.sha256"
	if sig != nil {
		rel.SigURL = srv.URL + "/asset.zip.sha256.sig"
	}
	return srv, rel
}

// withKeys подставляет ключ и режим на время теста. Значения глобальные, потому
// что приходят из ldflags; тесты не параллельные по той же причине.
func withKeys(t *testing.T, pub string, enforce string) {
	t.Helper()
	oldPub, oldEnforce := PublicKey, RequireSignature
	PublicKey, RequireSignature = pub, enforce
	t.Cleanup(func() { PublicKey, RequireSignature = oldPub, oldEnforce })
}

func signingKeys(t *testing.T) (pubB64 string, priv ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub), priv
}

func shaFileFor(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]) + "  asset.zip\n"
}

func TestDownload_ПодписанныйРелизПринимается(t *testing.T) {
	body := []byte("это архив обновления")
	shaBody := shaFileFor(body)
	pub, priv := signingKeys(t)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(shaBody))) + "\n")
	withKeys(t, pub, "1")

	_, rel := signedReleaseServer(t, body, shaBody, sig)
	path, err := Download(context.Background(), rel, t.TempDir())
	if err != nil {
		t.Fatalf("Download подписанного релиза: %v", err)
	}
	got, err := os.ReadFile(path) //nolint:gosec // G304: путь из теста
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("скачали %q, ждали %q", got, body)
	}
}

// Подпись чужим ключом — отказ. Это и есть тот случай, ради которого всё
// затевалось: у атакующего есть доступ к релизам, но нет приватного ключа.
func TestDownload_ЧужаяПодписьОтвергается(t *testing.T) {
	body := []byte("это архив обновления")
	shaBody := shaFileFor(body)
	pub, _ := signingKeys(t)
	_, foreignPriv := signingKeys(t)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(foreignPriv, []byte(shaBody))))
	withKeys(t, pub, "")

	dir := t.TempDir()
	_, rel := signedReleaseServer(t, body, shaBody, sig)
	_, err := Download(context.Background(), rel, dir)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("подпись чужим ключом принята: %v", err)
	}
	// Архив не должен быть скачан вовсе: подпись проверяется ДО закачки.
	if _, statErr := os.Stat(filepath.Join(dir, rel.AssetName)); !os.IsNotExist(statErr) {
		t.Fatalf("архив скачан несмотря на неверную подпись (err=%v)", statErr)
	}
}

// Подменённая сумма (а с ней и архив) ломает подпись — цепочка держится.
func TestDownload_ПодменаСуммыЛоматПодпись(t *testing.T) {
	body := []byte("это архив обновления")
	pub, priv := signingKeys(t)
	// Подписана честная сумма, а отдаётся сумма подменённого архива.
	honest := shaFileFor(body)
	evil := shaFileFor([]byte("подменённый архив"))
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(honest))))
	withKeys(t, pub, "")

	_, rel := signedReleaseServer(t, body, evil, sig)
	if _, err := Download(context.Background(), rel, t.TempDir()); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("подменённая сумма принята: %v", err)
	}
}

// Мягкий режим (переход): релиза без подписи достаточно, чтобы обновиться.
// Иначе первая же подписанная версия оборвала бы автообновление всем, кто
// стоит на старых сборках.
func TestDownload_БезПодписиМягкийРежимРазрешает(t *testing.T) {
	body := []byte("это архив обновления")
	shaBody := shaFileFor(body)
	pub, _ := signingKeys(t)
	withKeys(t, pub, "")

	_, rel := signedReleaseServer(t, body, shaBody, nil)
	if _, err := Download(context.Background(), rel, t.TempDir()); err != nil {
		t.Fatalf("мягкий режим обязан пропускать неподписанный релиз: %v", err)
	}
}

// Жёсткий режим: тот же релиз без подписи отвергается.
func TestDownload_БезПодписиЖёсткийРежимОтвергает(t *testing.T) {
	body := []byte("это архив обновления")
	shaBody := shaFileFor(body)
	pub, _ := signingKeys(t)
	withKeys(t, pub, "1")

	_, rel := signedReleaseServer(t, body, shaBody, nil)
	if _, err := Download(context.Background(), rel, t.TempDir()); !errors.Is(err, ErrSignatureMissing) {
		t.Fatalf("жёсткий режим принял неподписанный релиз: %v", err)
	}
}

// Сборка без вшитого ключа ведёт себя как до #783: собранный из исходников
// бинарь и форк со своим каналом обновлений не должны требовать ключей.
func TestDownload_БезКлючаПроверкиНет(t *testing.T) {
	body := []byte("это архив обновления")
	shaBody := shaFileFor(body)
	_, foreignPriv := signingKeys(t)
	// Подпись заведомо чужая — но проверять её нечем, и это не ошибка.
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(foreignPriv, []byte(shaBody))))
	withKeys(t, "", "1")

	_, rel := signedReleaseServer(t, body, shaBody, sig)
	if _, err := Download(context.Background(), rel, t.TempDir()); err != nil {
		t.Fatalf("без вшитого ключа обновление обязано работать как раньше: %v", err)
	}
}

// Испорченный ассет подписи — отказ, а не «считаем, что подписи нет».
func TestDownload_БитаяПодписьОтвергается(t *testing.T) {
	body := []byte("это архив обновления")
	shaBody := shaFileFor(body)
	pub, _ := signingKeys(t)
	withKeys(t, pub, "")

	_, rel := signedReleaseServer(t, body, shaBody, []byte("не base64 вовсе !!!"))
	if _, err := Download(context.Background(), rel, t.TempDir()); err == nil {
		t.Fatal("испорченная подпись принята")
	}
}

func TestSignatureEnforced(t *testing.T) {
	for _, on := range []string{"1", "true", "TRUE", "yes", "on"} {
		withKeys(t, "", on)
		if !SignatureEnforced() {
			t.Errorf("%q должно включать жёсткий режим", on)
		}
	}
	for _, off := range []string{"", "0", "false", "нет", "  "} {
		withKeys(t, "", off)
		if SignatureEnforced() {
			t.Errorf("%q не должно включать жёсткий режим", off)
		}
	}
}
