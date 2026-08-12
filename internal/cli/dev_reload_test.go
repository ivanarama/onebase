package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// devCmdWith собирает команду с настоящими флагами `onebase dev` и разбирает
// переданную строку аргументов.
func devCmdWith(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "dev", RunE: func(*cobra.Command, []string) error { return nil }}
	registerDevFlags(cmd)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("разбор флагов %v: %v", args, err)
	}
	return cmd
}

func argValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// С --sqlite дочерний процесс должен получить именно файл базы: строка
// подключения к PostgreSQL здесь не нужна и сбила бы выбор СУБД.
func TestDevChildArgs_PassesSQLite(t *testing.T) {
	cmd := devCmdWith(t, "--sqlite", "dev.db", "--project", "examples/trade", "--port", "8123")
	args := devChildArgs(cmd)

	if got, ok := argValue(args, "--sqlite"); !ok || got != "dev.db" {
		t.Errorf("--sqlite = %q (найден: %v), ожидался dev.db", got, ok)
	}
	if _, ok := argValue(args, "--db"); ok {
		t.Errorf("вместе с --sqlite передан --db: %v", args)
	}
	if got, ok := argValue(args, "--project"); !ok || got != "examples/trade" {
		t.Errorf("--project = %q, ожидался examples/trade", got)
	}
	if got, ok := argValue(args, "--port"); !ok || got != "8123" {
		t.Errorf("--port = %q, ожидался 8123", got)
	}
	if len(args) == 0 || args[0] != "dev" {
		t.Errorf("первым аргументом ожидалась подкоманда dev: %v", args)
	}
}

// Флаги самого супервизора дочернему процессу не передаются: --reload-binary
// запустил бы супервизор внутри супервизора, а --open открыл бы вторую вкладку.
func TestDevChildArgs_DropsSupervisorFlags(t *testing.T) {
	cmd := devCmdWith(t, "--reload-binary", "--open", "--source", "..", "--db", "postgres://localhost/onebase_dev")
	args := devChildArgs(cmd)

	joined := strings.Join(args, " ")
	for _, forbidden := range []string{"--reload-binary", "--open", "--source"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("дочернему процессу передан %s: %v", forbidden, args)
		}
	}
	if got, ok := argValue(args, "--db"); !ok || got != "postgres://localhost/onebase_dev" {
		t.Errorf("--db = %q, ожидалась строка подключения из флага", got)
	}
}

func TestDevChildArgs_DoesNotMaterializeDatabaseURLInArgv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:secret@db.example/onebase")
	cmd := devCmdWith(t, "--reload-binary")
	args := devChildArgs(cmd)
	if _, ok := argValue(args, "--db"); ok {
		t.Fatalf("inherited DATABASE_URL leaked into child argv: %v", args)
	}
	if strings.Contains(strings.Join(args, " "), "secret") {
		t.Fatalf("database password leaked into child argv: %v", args)
	}
}

func TestRunDev_RejectsDBAndSQLiteTogether(t *testing.T) {
	cmd := devCmdWith(t, "--db", "postgres://localhost/dev", "--sqlite", "dev.db")
	err := runDev(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("runDev error = %v, want mutually-exclusive flags error", err)
	}
}

func TestBrowserURLUsesReachableHost(t *testing.T) {
	for _, tc := range []struct {
		host string
		want string
	}{
		{"127.0.0.1", "http://127.0.0.1:8123"},
		{"0.0.0.0", "http://localhost:8123"},
		{"::", "http://localhost:8123"},
		{"::1", "http://[::1]:8123"},
		{"devbox.local", "http://devbox.local:8123"},
	} {
		if got := browserURL(tc.host, 8123); got != tc.want {
			t.Errorf("browserURL(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

func TestGoModuleRoot_FindsRootAbove(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "internal", "cli")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := goModuleRoot(nested)
	if err != nil {
		t.Fatalf("goModuleRoot: %v", err)
	}
	// t.TempDir на macOS отдаёт путь через симлинк /var → /private/var, поэтому
	// сравниваем разрешённые пути, а не строки как есть.
	wantResolved, _ := filepath.EvalSymlinks(root)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Errorf("корень модуля = %q, ожидался %q", got, root)
	}
}

func TestGoModuleRoot_ResolvesSymlink(t *testing.T) {
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real")
	nested := filepath.Join(realRoot, "internal", "cli")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realRoot, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "linked")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}

	got, err := goModuleRoot(filepath.Join(link, "internal", "cli"))
	if err != nil {
		t.Fatalf("goModuleRoot: %v", err)
	}
	want, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("module root = %q, want resolved path %q", got, want)
	}
}

// Без go.mod пересобирать нечего — вместо непонятной ошибки сборки разработчик
// должен получить объяснение, что запускать надо из дерева платформы.
func TestGoModuleRoot_ExplainsMissingModule(t *testing.T) {
	dir := t.TempDir()
	// Каталог верхнего уровня во временной папке — гарантированно без go.mod выше.
	_, err := goModuleRoot(dir)
	if err == nil {
		t.Fatal("ожидалась ошибка: go.mod нет ни в каталоге, ни выше")
	}
	if !strings.Contains(err.Error(), "--source") {
		t.Errorf("ошибка не подсказывает выход: %v", err)
	}
}
