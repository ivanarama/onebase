package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/ivantit66/onebase/internal/devserver"
	"github.com/ivantit66/onebase/internal/launcher"
	"github.com/spf13/cobra"
)

// runDevSupervisor — режим `onebase dev --reload-binary`: этот процесс сервер не
// поднимает, а собирает платформу из исходников и держит её дочерним процессом,
// пересобирая на каждую правку Go-кода. Нужен разработчику самой платформы;
// разработчику конфигурации хватает обычного `onebase dev`, где .os/.yaml
// перечитываются без пересборки.
func runDevSupervisor(cmd *cobra.Command) error {
	source, _ := cmd.Flags().GetString("source")
	srcDir, err := goModuleRoot(source)
	if err != nil {
		return err
	}
	if _, err := devserver.GoTool(); err != nil {
		return fmt.Errorf("--reload-binary требует компилятор Go: %w", err)
	}
	port, _ := cmd.Flags().GetInt("port")
	openBrowser, _ := cmd.Flags().GetBool("open")

	sup := &devserver.Supervisor{
		SourceDir: srcDir,
		Args:      devChildArgs(cmd),
		Port:      port,
		Out:       os.Stdout,
		OnReady: func(restart bool) {
			// Браузер открываем только на первом запуске: после пересборки
			// страница обновляется сама (см. dev.поколение в ui.js), а новая
			// вкладка на каждую правку кода — это не помощь, а помеха.
			if restart || !openBrowser {
				return
			}
			openInBrowser("127.0.0.1", port)
		},
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return sup.Run(ctx)
}

// devChildArgs собирает аргументы дочернего `onebase dev`. Собираются явно, а не
// пробросом os.Args: дочерний процесс не должен унаследовать ни --reload-binary
// (иначе супервизор запустил бы супервизор), ни --open (браузер открывает
// родитель, и ровно один раз).
func devChildArgs(cmd *cobra.Command) []string {
	dir, _ := cmd.Flags().GetString("project")
	port, _ := cmd.Flags().GetInt("port")
	configSource, _ := cmd.Flags().GetString("config-source")
	sqlitePath, _ := cmd.Flags().GetString("sqlite")

	args := []string{"dev", "--project", dir, "--port", strconv.Itoa(port), "--config-source", configSource}
	if sqlitePath != "" {
		// Передаём то же, что выбрал разработчик: с --sqlite строка подключения
		// к PostgreSQL дочернему процессу не нужна и только сбила бы выбор СУБД.
		return append(args, "--sqlite", sqlitePath)
	}
	// DATABASE_URL stays in the inherited environment. Copying it to argv would
	// expose the password in process listings. Only an explicitly supplied
	// command-line value remains a command-line value in the child.
	if cmd.Flags().Changed("db") {
		dsn, _ := cmd.Flags().GetString("db")
		if dsn != "" {
			return append(args, "--db", dsn)
		}
	}
	return args
}

// goModuleRoot поднимается от start вверх до каталога с go.mod — это корень
// дерева, которое пересобирает супервизор.
func goModuleRoot(start string) (string, error) {
	if start == "" {
		start = "."
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("--source: %w", err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("--source: разрешить символические ссылки: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod не найден в %s и выше: --reload-binary пересобирает платформу из исходников, "+
				"запускайте его из дерева платформы или укажите --source", start)
		}
		dir = parent
	}
}

// openInBrowser открывает UI базы во внешнем браузере.
func browserURL(host string, port int) string {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	switch host {
	case "", "0.0.0.0", "::":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

func openInBrowser(host string, port int) {
	address := browserURL(host, port)
	outf("[open] открываю %s\n", address)
	launcher.OpenBrowser(address)
}
