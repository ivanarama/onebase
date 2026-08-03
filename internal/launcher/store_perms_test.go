package launcher

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Реестр баз хранит поле db — строку подключения к PostgreSQL вместе с паролем.
// Пока файл писался с 0644, её мог прочитать любой локальный пользователь
// машины. Тест держит режим 0600: это не косметика, а единственное, что
// отделяет пароль от соседней учётки.
func TestStoreFileIsNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("права POSIX на Windows не проверяются")
	}
	st := newTestStore(t)
	if err := st.Add(&Base{
		ID: "b", Name: "Рабочая", ConfigSource: "file",
		DB: "postgres://onebase:СуперПароль@localhost/onebase",
	}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(st.path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("режим реестра = %04o, ожидался 0600: в файле лежит пароль от БД", mode)
	}

	// Заодно убеждаемся, что пароль там действительно есть — иначе тест
	// проверял бы права у файла, ради которого их и не стоило бы менять.
	raw, err := os.ReadFile(st.path) //nolint:gosec // G304: путь получен здесь же
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "СуперПароль") {
		t.Fatal("в реестре не оказалось строки подключения — тест потерял смысл")
	}
}

// Каталог ~/.onebase принадлежит одному пользователю: сервис, установленный
// через `onebase service install --user`, работает со своим HOME и сюда не
// заглядывает.
func TestOnebaseDirIsPrivateOnCreate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("права POSIX на Windows не проверяются")
	}
	t.Setenv("HOME", t.TempDir())

	if _, err := NewStore(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".onebase")) //nolint:gosec // G703: HOME задан этим же тестом через t.Setenv
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("режим каталога = %04o, ожидался 0700", mode)
	}
}
