package i18n

import (
	"os"
	"path/filepath"
	"testing"
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

func TestT_UnknownLang_ReturnsKey(t *testing.T) {
	b, err := Load(EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	got := b.T("xx", "Записать")
	if got != "Записать" {
		t.Errorf("T(xx, Записать) = %q, want key back", got)
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
