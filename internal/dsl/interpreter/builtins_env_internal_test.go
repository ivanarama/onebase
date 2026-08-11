package interpreter

import (
	"errors"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

func TestTempFileFunctionsCoveredByFileGuard(t *testing.T) {
	deny := FileGuard(func() error { return errors.New("файлы запрещены") })
	functions := NewFileFunctions(deny)
	for _, name := range []string{
		"каталогвременныхфайлов", "tempfilesdir",
		"получитьимявременногофайла", "gettempfilename",
	} {
		if msg := callBuiltinExpectPanic(t, functions[name], nil); !strings.Contains(msg, "файлы запрещены") {
			t.Errorf("%s: guard вернул %q", name, msg)
		}
	}
}

func TestTempFileNameRejectsPathSyntaxAndUsesRandomToken(t *testing.T) {
	SetFileSandbox("")
	fn, ok := NewFileFunctions(nil)["получитьимявременногофайла"].(BuiltinFunc)
	if !ok {
		t.Fatal("получитьимявременногофайла должна быть BuiltinFunc")
	}

	invalid := []string{
		"../../../../", `..\..\secret`, "x/y", `x\y`, "x:y", "x\x00y",
		"..log", "a b", strings.Repeat("a", 65),
	}
	for _, ext := range invalid {
		if got, err := fn([]any{ext}, "test.os", 1); err == nil {
			t.Errorf("расширение %q принято: %v", ext, got)
		}
	}

	namePattern := regexp.MustCompile(`^onebase-[0-9a-f]{32}\.tar\.gz$`)
	seen := make(map[string]struct{}, 128)
	for i := 0; i < 128; i++ {
		got, err := fn([]any{"tar.gz"}, "test.os", 1)
		if err != nil {
			t.Fatalf("валидное расширение: %v", err)
		}
		path, ok := got.(string)
		if !ok {
			t.Fatalf("результат %T, ожидалась строка", got)
		}
		if filepath.Dir(path) != os.TempDir() || !namePattern.MatchString(filepath.Base(path)) {
			t.Fatalf("неожиданное временное имя %q", path)
		}
		if _, duplicate := seen[path]; duplicate {
			t.Fatalf("повторное временное имя %q", path)
		}
		seen[path] = struct{}{}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("функция создала файл %q: %v", path, err)
		}
	}
}

func TestTempDirDoesNotHideCreationError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	SetFileSandbox(root)
	defer SetFileSandbox("")
	if got, err := tempDirFn(nil, "test.os", 1); err == nil {
		t.Fatalf("ошибка MkdirAll скрыта, получено %v", got)
	}
}

func TestURLDecodeNeverReturnsInvalidUTF8(t *testing.T) {
	got, err := decodeStringFn([]any{"%FF"}, "test.os", 1)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := got.(string)
	if !ok || text != "%FF" || !utf8.ValidString(text) {
		t.Fatalf("получено %#v, ожидалась исходная валидная строка", got)
	}
}

func TestURLModeTypoRejected(t *testing.T) {
	if got, err := encodeStringFn([]any{"a/b", "URLВКодировкеUR1"}, "test.os", 1); err == nil {
		t.Fatalf("неизвестный режим принят: %v", got)
	}
	if got, err := decodeStringFn([]any{"a%2Fb", "URLВКодировкеUR1"}, "test.os", 1); err == nil {
		t.Fatalf("неизвестный режим decode принят: %v", got)
	}
}

func TestDecimalQuotientSafetyEnvelope(t *testing.T) {
	for _, d := range []decimal.Decimal{
		decimal.New(1, math.MaxInt32),
		decimal.New(1, math.MinInt32),
		decimal.NewFromBigInt(new(big.Int).Lsh(big.NewInt(1), maxDecimalQuotientCoefficientBits), 0),
	} {
		if decimalSafeForQuotient(d) {
			t.Fatalf("опасный Decimal признан безопасным: exponent=%d bits=%d", d.Exponent(), d.Coefficient().BitLen())
		}
	}

	boundary := decimal.NewFromBigInt(
		new(big.Int).Lsh(big.NewInt(1), maxDecimalQuotientCoefficientBits-1),
		maxDecimalQuotientExponent,
	)
	if !decimalSafeForQuotient(boundary) {
		t.Fatalf("допустимая граница отклонена: exponent=%d bits=%d", boundary.Exponent(), boundary.Coefficient().BitLen())
	}
}
