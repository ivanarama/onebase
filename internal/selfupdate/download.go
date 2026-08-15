package selfupdate

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/version"
)

// Клиенты намеренно разные: у метаданных короткий таймаут (проверка обновлений
// не должна подвешивать старт лаунчера), у загрузки — длинный, архив весит
// десятки мегабайт. Транспорт по умолчанию уважает HTTP_PROXY/HTTPS_PROXY/
// NO_PROXY — в корпоративном контуре это единственный способ выйти наружу.
func apiClient() *http.Client { return &http.Client{Timeout: 20 * time.Second} }

func downloadClient() *http.Client { return &http.Client{Timeout: 15 * time.Minute} }

// userAgent — GitHub API отклоняет запросы без User-Agent.
func userAgent() string { return "onebase/" + version.String() }

// maxSHAFileBytes — файл контрольной суммы это одна строка; всё, что больше,
// заведомо не он.
const maxSHAFileBytes = 4 << 10

// maxSignatureBytes — подпись Ed25519 в base64 это 88 символов; запас на
// перевод строки и BOM.
const maxSignatureBytes = 1 << 10

// Download скачивает архив релиза в destDir и сверяет его контрольную сумму с
// опубликованной рядом .sha256. Возвращает путь к проверенному архиву.
//
// Цепочка доверия: подпись подтверждает файл контрольной суммы, сумма
// подтверждает архив. Одной суммы мало — она лежит на том же GitHub, что и
// архив, и тот, кто получил доступ к репозиторию, подменяет обе (#783).
//
// Подпись проверяется, если в сборку вшит открытый ключ. Её отсутствие в
// релизе на этом этапе допустимо (мягкий переход): жёсткий отказ включается
// сборкой с RequireSignature. Неверная подпись — отказ всегда.
func Download(ctx context.Context, rel Release, destDir string) (string, error) {
	if err := os.MkdirAll(destDir, fsmode.SecretDir); err != nil {
		return "", err
	}
	// Сначала сумма: она маленькая, и если её нет — незачем тянуть десятки
	// мегабайт, которые всё равно нельзя будет применить.
	shaRaw, err := fetchBytes(ctx, rel.SHAURL, maxSHAFileBytes)
	if err != nil {
		return "", fmt.Errorf("selfupdate: скачать контрольную сумму: %w", err)
	}
	if err := checkReleaseSignature(ctx, rel, shaRaw); err != nil {
		return "", err
	}
	want, err := ParseSHAFile(shaRaw)
	if err != nil {
		return "", err
	}

	archive := filepath.Join(destDir, rel.AssetName)
	if err := downloadFile(ctx, rel.AssetURL, archive); err != nil {
		return "", err
	}
	if err := VerifySHA256(archive, want); err != nil {
		// Битую загрузку не оставляем на диске: иначе повторная попытка
		// наткнётся на неё же и снова упадёт.
		_ = os.Remove(archive)
		return "", err
	}
	return archive, nil
}

// checkReleaseSignature проверяет подпись файла контрольной суммы.
//
// Три состояния, и они намеренно разные:
//   - ключ не вшит — проверять нечем, поведение как до #783 (сборка из
//     исходников, форк со своим каналом обновлений);
//   - подписи нет в релизе — отказ только в жёстком режиме; иначе строка в
//     журнал, чтобы отсутствие подписи не было тихим;
//   - подпись есть и не сходится — отказ всегда, даже в мягком режиме. Мягкость
//     касается отсутствия подписи, а не её несовпадения.
func checkReleaseSignature(ctx context.Context, rel Release, shaRaw []byte) error {
	if !SignatureConfigured() {
		return nil
	}
	if rel.SigURL == "" {
		if SignatureEnforced() {
			return fmt.Errorf("%w: в релизе %s нет %s.sha256.sig", ErrSignatureMissing, rel.Tag, rel.AssetName)
		}
		slog.Warn("релиз не подписан — обновление продолжено (мягкий режим)",
			"component", "selfupdate", "релиз", rel.Tag, "архив", rel.AssetName)
		return nil
	}
	sig, err := fetchBytes(ctx, rel.SigURL, maxSignatureBytes)
	if err != nil {
		return fmt.Errorf("selfupdate: скачать подпись релиза: %w", err)
	}
	if err := VerifySignature(shaRaw, sig); err != nil {
		return err
	}
	return nil
}

func downloadFile(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := downloadClient().Do(req)
	if err != nil {
		return fmt.Errorf("selfupdate: скачать %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("selfupdate: %s вернул %d", url, resp.StatusCode)
	}
	if resp.ContentLength > MaxBinaryBytes {
		return fmt.Errorf("selfupdate: архив обновления превышает лимит %d байт", MaxBinaryBytes)
	}
	// +1 к лимиту, чтобы отличить «ровно лимит» от «больше лимита».
	if err := writeFile(io.LimitReader(resp.Body, MaxBinaryBytes+1), dst, fsmode.File); err != nil {
		return err
	}
	info, err := os.Stat(dst)
	if err != nil {
		return err
	}
	if info.Size() > MaxBinaryBytes {
		_ = os.Remove(dst)
		return fmt.Errorf("selfupdate: архив обновления превышает лимит %d байт", MaxBinaryBytes)
	}
	return nil
}

func fetchBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := apiClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s вернул %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// ParseSHAFile достаёт сумму из файла .sha256. Форматов два, оба наши:
//
//	sha256sum (Linux/macOS):   "<hex>  onebase-linux-amd64.tar.gz\n"
//	Get-FileHash (Windows):    "<hex>  onebase-windows-amd64.zip\r\n"
//
// Отличаются переводом строки и регистром (PowerShell отдаёт верхний, но
// release.yml приводит его к нижнему). Отдельно терпим форму sha256sum -b
// («<hex> *имя») и голую сумму без имени файла.
func ParseSHAFile(b []byte) (string, error) {
	// UTF-8 BOM: Out-File с некоторыми кодировками его добавляет, и тогда
	// первый «символ» суммы оказывается не hex.
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		sum := strings.TrimPrefix(fields[0], "*")
		decoded, err := hex.DecodeString(sum)
		if err != nil || len(decoded) != 32 {
			return "", fmt.Errorf("selfupdate: в файле контрольной суммы ожидались 64 шестнадцатеричных символа, получено %q", fields[0])
		}
		return strings.ToLower(sum), nil
	}
	return "", fmt.Errorf("selfupdate: файл контрольной суммы пуст")
}
