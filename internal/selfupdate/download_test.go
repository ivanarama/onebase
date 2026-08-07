package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSHAFile(t *testing.T) {
	sum := "5f2b1c9d4e3a6b8c0d1e2f3a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e"
	cases := []struct {
		name string
		in   string
		want string
	}{
		// sha256sum на Linux/macOS.
		{"unix", sum + "  onebase-linux-amd64.tar.gz\n", sum},
		// Get-FileHash + Out-File на Windows: CRLF в конце.
		{"windows crlf", sum + "  onebase-windows-amd64.zip\r\n", sum},
		// PowerShell отдаёт верхний регистр; release.yml его понижает, но
		// полагаться на это не будем.
		{"верхний регистр", "5F2B1C9D4E3A6B8C0D1E2F3A4B5C6D7E8F90A1B2C3D4E5F60718293A4B5C6D7E  a.zip", sum},
		{"бинарный режим sha256sum", sum + " *onebase\n", sum},
		{"голая сумма", sum, sum},
		{"BOM в начале", "\xEF\xBB\xBF" + sum + "  a.zip\n", sum},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseSHAFile([]byte(c.in))
			if err != nil {
				t.Fatalf("ParseSHAFile: %v", err)
			}
			if got != c.want {
				t.Fatalf("получили %q, ждали %q", got, c.want)
			}
		})
	}
}

func TestParseSHAFile_Invalid(t *testing.T) {
	for _, in := range []string{"", "\n\n", "не-сумма  a.zip", "abcdef  a.zip"} {
		if _, err := ParseSHAFile([]byte(in)); err == nil {
			t.Fatalf("для %q ждали ошибку", in)
		}
	}
}

// releaseServer отдаёт архив и файл контрольной суммы, как это делает GitHub.
func releaseServer(t *testing.T, body []byte, shaBody string) (*httptest.Server, Release) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/asset.zip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/asset.zip.sha256", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(shaBody))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, Release{
		Tag:       "build-672",
		AssetName: "asset.zip",
		AssetURL:  srv.URL + "/asset.zip",
		SHAURL:    srv.URL + "/asset.zip.sha256",
	}
}

func TestDownload_VerifiesChecksum(t *testing.T) {
	body := []byte("это архив обновления")
	sum := sha256.Sum256(body)
	_, rel := releaseServer(t, body, hex.EncodeToString(sum[:])+"  asset.zip\n")

	dir := t.TempDir()
	path, err := Download(context.Background(), rel, dir)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(path) //nolint:gosec // G304: путь из теста
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("скачали %q, ждали %q", got, body)
	}
}

func TestDownload_ChecksumMismatchRemovesFile(t *testing.T) {
	body := []byte("это архив обновления")
	wrong := sha256.Sum256([]byte("совсем другое"))
	_, rel := releaseServer(t, body, hex.EncodeToString(wrong[:])+"  asset.zip\n")

	dir := t.TempDir()
	if _, err := Download(context.Background(), rel, dir); err == nil {
		t.Fatal("ждали ошибку несовпадения контрольной суммы")
	}
	// Битую загрузку нельзя оставлять: повторная попытка наткнулась бы на неё.
	if _, err := os.Stat(filepath.Join(dir, rel.AssetName)); !os.IsNotExist(err) {
		t.Fatalf("файл с несошедшейся суммой остался на диске (err=%v)", err)
	}
}

func TestDownload_MissingChecksumFails(t *testing.T) {
	body := []byte("архив")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if filepath.Ext(r.URL.Path) == ".sha256" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	rel := Release{Tag: "build-1", AssetName: "asset.zip", AssetURL: srv.URL + "/asset.zip", SHAURL: srv.URL + "/asset.zip.sha256"}
	if _, err := Download(context.Background(), rel, t.TempDir()); err == nil {
		t.Fatal("без контрольной суммы обновление скачиваться не должно")
	}
}
