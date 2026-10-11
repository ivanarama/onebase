package secrets

import (
	"errors"
	"testing"
)

// ResolveEnc разворачивает enc: — целиком и встроенный в строку — и оставляет
// значение без ссылок как есть.
func TestResolveEncOnlyEnc(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := key.Encrypt("пароль-1С")
	if err != nil {
		t.Fatal(err)
	}
	r := testResolver(t, nil, WithKey(key))
	for in, want := range map[string]string{
		ref:                    "пароль-1С",
		"Basic ${" + ref + "}": "Basic пароль-1С",
		"пароль открытым текстом": "пароль открытым текстом",
		"": "",
	} {
		got, err := r.ResolveEnc(in)
		if err != nil {
			t.Fatalf("ResolveEnc(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("ResolveEnc(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

// env:/file: из данных базы отклоняются до разыменования: переменная окружения
// не читается, файл не открывается, даже если рядом стоит законная enc:-ссылка.
func TestResolveEncRejectsEnvAndFile(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := key.Encrypt("x")
	if err != nil {
		t.Fatal(err)
	}
	read := false
	r := New(
		WithEnv(func(string) string { read = true; return "утечка" }),
		WithFileReader(func(string) ([]byte, error) { read = true; return []byte("утечка"), nil }),
		WithKey(key),
	)
	for _, in := range []string{
		"env:DATABASE_URL",
		" file:/etc/passwd",
		"${env:ONEBASE_MASTER_KEY}",
		"${" + ref + "}${file:/etc/passwd}",
	} {
		got, err := r.ResolveEnc(in)
		if !errors.Is(err, ErrRefNotAllowed) {
			t.Fatalf("ResolveEnc(%q): ожидалась ErrRefNotAllowed, получено %q, %v", in, got, err)
		}
		if got != "" {
			t.Fatalf("ResolveEnc(%q) вернул значение %q вместе с ошибкой", in, got)
		}
	}
	if read {
		t.Fatal("ResolveEnc обратился к окружению или файлу")
	}
}
