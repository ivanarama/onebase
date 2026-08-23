package i18nerr

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ivantit66/onebase/internal/i18n"
)

func testBundle(t *testing.T) *i18n.Bundle {
	t.Helper()
	fsys := fstest.MapFS{
		"en.json": &fstest.MapFile{Data: []byte(`{
			"неизвестная таблица %s": "unknown table %s",
			"Деление на ноль": "Division by zero",
			"сохранение документа": "saving document"
		}`)},
	}
	b, err := i18n.Load(fsys, "")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return b
}

func TestErrorRendersRussian(t *testing.T) {
	err := Errorf("неизвестная таблица %s", "товары")
	if err.Error() != "неизвестная таблица товары" {
		t.Fatalf("Error() = %q", err.Error())
	}
}

func TestLocalizeTemplate(t *testing.T) {
	b := testBundle(t)
	err := Errorf("неизвестная таблица %s", "товары")
	if got := Localize(b, "en", err); got != "unknown table товары" {
		t.Fatalf("Localize = %q", got)
	}
	// ru — язык самих сообщений, текст без изменений.
	if got := Localize(b, "ru", err); got != "неизвестная таблица товары" {
		t.Fatalf("Localize ru = %q", got)
	}
	// Язык без словаря откатывается на английский, а не на русский (#960).
	if got := Localize(b, "zz", err); got != "unknown table товары" {
		t.Fatalf("Localize zz = %q", got)
	}
}

func TestLocalizeExactMatchForPlainErrors(t *testing.T) {
	b := testBundle(t)
	err := errors.New("Деление на ноль")
	if got := Localize(b, "en", err); got != "Division by zero" {
		t.Fatalf("Localize = %q", got)
	}
	// Непереводимое — как есть.
	err2 := errors.New("что-то совсем другое")
	if got := Localize(b, "en", err2); got != "что-то совсем другое" {
		t.Fatalf("Localize = %q", got)
	}
}

func TestLocalizeWrappedChain(t *testing.T) {
	b := testBundle(t)
	inner := Errorf("неизвестная таблица %s", "товары")
	outer := Wrapf(inner, "сохранение документа")
	if outer.Error() != "сохранение документа: неизвестная таблица товары" {
		t.Fatalf("Error() = %q", outer.Error())
	}
	if got := Localize(b, "en", outer); got != "saving document: unknown table товары" {
		t.Fatalf("Localize = %q", got)
	}
	// Обёртка через fmt.Errorf %w: i18nerr-звено переводится, префикс остаётся.
	wrapped := fmt.Errorf("контекст: %w", inner)
	if got := Localize(b, "en", wrapped); got != "контекст: unknown table товары" {
		t.Fatalf("Localize = %q", got)
	}
	// errors.Is работает сквозь цепочку.
	if !errors.Is(outer, inner) {
		t.Fatalf("errors.Is не видит wrapped")
	}
}

// errors.Join: у мультиошибки Unwrap() отдаёт срез, а не одну ошибку, поэтому
// без отдельной ветки перевод сваливался бы в exact-match по склеенному тексту
// и не находил ничего. Так миграция называет сразу все объекты, не прошедшие
// предусловие уникальности (#1080).
func TestLocalizeJoinedErrors(t *testing.T) {
	b := testBundle(t)
	joined := errors.Join(
		Errorf("неизвестная таблица %s", "товары"),
		New("Деление на ноль"),
	)
	want := "unknown table товары\nDivision by zero"
	if got := Localize(b, "en", joined); got != want {
		t.Fatalf("Localize = %q, ждали %q", got, want)
	}
	// Одна причина в Join — тот же перевод, что и без него.
	single := errors.Join(Errorf("неизвестная таблица %s", "склады"))
	if got := Localize(b, "en", single); got != "unknown table склады" {
		t.Fatalf("Localize одиночного Join = %q", got)
	}
}

// Перевод внешнего звена, содержащий русский рендер внутреннего как
// подстроку, не должен портить сборку (структурная локализация vs Replace).
func TestLocalizeChainNoFalseSubstitution(t *testing.T) {
	fsys := fstest.MapFS{
		"en.json": &fstest.MapFile{Data: []byte(`{
			"обработка %s": "обж-process %s",
			"обж": "ozh-inner"
		}`)},
	}
	b, err := i18n.Load(fsys, "")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	chain := Wrapf(New("обж"), "обработка %s", "y")
	if got := Localize(b, "en", chain); got != "обж-process y: ozh-inner" {
		t.Fatalf("Localize = %q", got)
	}
}

// Кривой перевод (несовпадение fmt-verbs) деградирует изящно — артефакты
// fmt (%!s(MISSING), EXTRA), но не паника. Пиннинг текущего поведения.
func TestLocalizeVerbMismatchDegradesGracefully(t *testing.T) {
	fsys := fstest.MapFS{
		"en.json": &fstest.MapFile{Data: []byte(`{
			"таблица %s": "table %s of %s"
		}`)},
	}
	b, err := i18n.Load(fsys, "")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := Localize(b, "en", Errorf("таблица %s", "x"))
	if !strings.Contains(got, "table x") || !strings.Contains(got, "MISSING") {
		t.Fatalf("ожидалась изящная деградация fmt, получено %q", got)
	}
}
