package i18n

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestT_ReturnsTranslation(t *testing.T) {
	b, err := Load(EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	got := b.T("en", "Записать")
	if got != "Save" {
		t.Errorf("T(en, Записать) = %q, want %q", got, "Save")
	}
}

func TestT_UnknownKey_ReturnsKey(t *testing.T) {
	b, err := Load(EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	got := b.T("en", "Несуществующий ключ")
	if got != "Несуществующий ключ" {
		t.Errorf("T(en, Несуществующий ключ) = %q, want key back", got)
	}
}

// Язык без словаря получает английский, а не русский ключ: русский текст в
// чужом интерфейсе нечитаем (#960). Раньше тест фиксировал обратное.
func TestT_UnknownLang_FallsBackToEnglish(t *testing.T) {
	b, err := Load(EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	got := b.T("xx", "Записать")
	if got != "Save" {
		t.Errorf("T(xx, Записать) = %q, want English fallback %q", got, "Save")
	}
}

// Русский — язык ключей: английский откат для него был бы регрессией.
// Проверяем и региональный вариант: Resolve пропускает явный выбор
// пользователя как есть, поэтому «ru-RU» доходит до T() без нормализации.
func TestT_BaseLang_KeepsKey(t *testing.T) {
	b, err := Load(EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"ru", "ru-ru"} {
		if got := b.T(lang, "Записать"); got != "Записать" {
			t.Errorf("T(%s, Записать) = %q, want key back", lang, got)
		}
	}
}

// Непереведённый ключ в живой локали отдаётся по-английски — это и есть
// принятая по #960 норма вместо накопления долга.
func TestT_PartialLocale_FallsBackToEnglish(t *testing.T) {
	b, err := Load(EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	const key = "Несуществующий в hy ключ"
	// Ключ, которого заведомо нет ни в одной локали, возвращается как есть.
	if got := b.T("hy", key); got != key {
		t.Errorf("T(hy, %q) = %q, want key back", key, got)
	}
	// А ключ, который есть только в en, отдаётся по-английски.
	en := b.Dict("en")
	hy := b.dicts["hy"]
	for k, v := range en {
		if _, ok := hy[k]; ok {
			continue
		}
		if got := b.T("hy", k); got != v {
			t.Errorf("T(hy, %q) = %q, want English %q", k, got, v)
		}
		return
	}
}

// Региональный вариант берёт словарь своего базового языка.
func TestT_RegionalVariant_UsesBaseSubtag(t *testing.T) {
	b, err := Load(EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	want := b.T("de", "Записать")
	if got := b.T("de-at", "Записать"); got != want {
		t.Errorf("T(de-at, Записать) = %q, want %q", got, want)
	}
}

// Словарь для фронтенда несёт ту же цепочку отката, что и T(): клиент умеет
// только dict[k] || k, поэтому английский должен быть уже внутри словаря.
func TestDict_CarriesEnglishFallback(t *testing.T) {
	b, err := Load(EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	en := b.Dict("en")
	hy := b.Dict("hy")
	for k, v := range en {
		if _, ok := b.dicts["hy"][k]; ok {
			continue
		}
		if hy[k] != v {
			t.Errorf("Dict(hy)[%q] = %q, want English %q", k, hy[k], v)
		}
		break
	}
	// Русский словарь английским не разбавляется.
	ru := b.Dict("ru")
	if len(ru) > len(b.dicts["ru"]) {
		t.Errorf("Dict(ru) = %d ключей, ожидалось не больше %d — английский подмешан в язык ключей", len(ru), len(b.dicts["ru"]))
	}
}

// Человеческий перевод побеждает машинный независимо от места подкаталога
// «machine» в лексическом обходе: fs.WalkDir прошёл бы locales/az.json ДО
// locales/machine/az.json и ПОСЛЕ locales/machine/uz.json, то есть половина
// языков получила бы машинный перевод поверх проверенного.
func TestMachineTierLoadsUnderHuman(t *testing.T) {
	fsys := fstest.MapFS{
		"locales/az.json":         &fstest.MapFile{Data: []byte(`{"Записать":"человек-az"}`)},
		"locales/machine/az.json": &fstest.MapFile{Data: []byte(`{"Записать":"машина-az","Удалить":"машина-az-2"}`)},
		"locales/uz.json":         &fstest.MapFile{Data: []byte(`{"Записать":"человек-uz"}`)},
		"locales/machine/uz.json": &fstest.MapFile{Data: []byte(`{"Записать":"машина-uz"}`)},
	}
	b, err := Load(fsys, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"az", "uz"} {
		if got := b.T(lang, "Записать"); got != "человек-"+lang {
			t.Errorf("T(%s, Записать) = %q, машинный перевод перекрыл человеческий", lang, got)
		}
	}
	// Ключ, которого у человека нет, берётся из машинного яруса.
	if got := b.T("az", "Удалить"); got != "машина-az-2" {
		t.Errorf("T(az, Удалить) = %q, want машинный перевод", got)
	}
}

// Машинный ярус должен попасть во встроенную ФС. Проверка нужна именно на
// EmbeddedLocales, а не на fstest: шаблон //go:embed легко потерять при правке
// (locales/*.json не захватывает подкаталог), сборка при этом останется зелёной,
// а интерфейс тихо съедет на английский на всех языках сразу.
func TestEmbeddedMachineTierIsPresent(t *testing.T) {
	b, err := Load(EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	machine, err := fs.Glob(EmbeddedLocales, "locales/"+MachineDir+"/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(machine) == 0 {
		t.Fatal("машинные словари не встроены — проверьте шаблон //go:embed")
	}
	for _, f := range machine {
		data, err := fs.ReadFile(EmbeddedLocales, f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		lang := strings.TrimSuffix(filepath.Base(f), ".json")
		for k, v := range m {
			if got := b.T(lang, k); got != v {
				t.Errorf("T(%s, %q) = %q, машинный перевод %q не доехал", lang, k, got, v)
			}
			break // одного ключа на язык достаточно: проверяем факт слияния
		}
	}
}

func TestExternalOverrides(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"Записать": "Speichern", "__native__": "Deutsch"}`)
	if err := os.WriteFile(filepath.Join(dir, "de.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	b, err := Load(EmbeddedLocales, dir)
	if err != nil {
		t.Fatal(err)
	}
	got := b.T("de", "Записать")
	if got != "Speichern" {
		t.Errorf("T(de, Записать) = %q, want %q", got, "Speichern")
	}
	// en should still work
	got = b.T("en", "Записать")
	if got != "Save" {
		t.Errorf("T(en, Записать) = %q after external load, want %q", got, "Save")
	}
}

func TestAvailable(t *testing.T) {
	b, err := Load(EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	avail := b.Available()
	found := false
	for _, l := range avail {
		if l.Code == "en" {
			found = true
			if l.Native != "English" {
				t.Errorf("en native = %q, want %q", l.Native, "English")
			}
		}
	}
	if !found {
		t.Error("en not in Available()")
	}
}

func TestResolve(t *testing.T) {
	b, err := Load(EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		user, base, accept, want string
	}{
		{"en", "", "", "en"},
		{"", "en", "", "en"},
		{"", "", "en-US,en;q=0.9", "en"},
		{"", "", "", "ru"},
		{"", "ru", "", "ru"},
		{"de", "en", "", "de"},  // explicit user choice accepted as-is
		{"", "", "de,en", "de"}, // de is loaded → picks first Accept-Language match
	}
	for _, tt := range tests {
		got := Resolve(tt.user, tt.base, tt.accept, b)
		if got != tt.want {
			t.Errorf("Resolve(%q,%q,%q) = %q, want %q", tt.user, tt.base, tt.accept, got, tt.want)
		}
	}
}

func TestResolve_Normalization(t *testing.T) {
	b, err := Load(EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	got := Resolve("EN-US", "", "", b)
	if got != "en-us" {
		t.Errorf("Resolve(EN-US) = %q, want %q", got, "en-us")
	}
	got = Resolve("  en  ", "", "", b)
	if got != "en" {
		t.Errorf("Resolve('  en  ') = %q, want %q", got, "en")
	}
}
