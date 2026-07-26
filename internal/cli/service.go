package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/ivantit66/onebase/internal/launcher"
	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage onebase as a system service",
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install onebase as a system service (systemd on Linux, sc.exe on Windows)",
	Long: `Install onebase as a system service (systemd on Linux, sc.exe on Windows).

На Windows сервис запускается от LocalSystem. Эта учётная запись не видит
сетевые диски, подключённые в пользовательской сессии (Z:, X: и т.п.). Для
проекта и SQLite используйте локальный путь или UNC (\\server\share\...).`,
	Example: `  onebase service install --id <base-id>
  onebase service install --db "postgres://..." --port 8080 --name myapp
  onebase service install --sqlite ./base.db --project ./project --config-source file --port 8080 --name myapp`,
	RunE: runServiceInstall,
}

var serviceUninstallCmd = &cobra.Command{
	Use:     "uninstall",
	Short:   "Remove the onebase system service",
	Example: `  onebase service uninstall --name onebase-myapp`,
	RunE:    runServiceUninstall,
}

func init() {
	serviceInstallCmd.Flags().String("id", "", "base ID from ibases registry")
	serviceInstallCmd.Flags().String("name", "", "service name (default: onebase-<base-name>)")
	serviceInstallCmd.Flags().String("db", "", "PostgreSQL DSN (if not using --id)")
	serviceInstallCmd.Flags().String("sqlite", "", "SQLite database file path (if not using --id)")
	serviceInstallCmd.Flags().Int("port", 8080, "HTTP port (if not using --id)")
	serviceInstallCmd.Flags().String("config-source", "database", "file or database (if not using --id)")
	serviceInstallCmd.Flags().String("project", "", "project directory (for file config-source)")
	serviceInstallCmd.Flags().String("user", "", "system user to run the service (Linux only, default: current user)")
	serviceInstallCmd.Flags().Bool("print", false, "print the unit file instead of installing it")
	serviceInstallCmd.Flags().Bool("watch", false, "запускать сервер с --watch (hot reload metadata/DSL/scheduled без рестарта)")

	serviceUninstallCmd.Flags().String("name", "onebase", "service name to remove")

	serviceCmd.AddCommand(serviceInstallCmd, serviceUninstallCmd)
}

// ── install ───────────────────────────────────────────────────────────────────

func runServiceInstall(cmd *cobra.Command, _ []string) error {
	baseID, _ := cmd.Flags().GetString("id")
	svcName, _ := cmd.Flags().GetString("name")
	printOnly, _ := cmd.Flags().GetBool("print")

	var dsn, sqlitePath, dbType, configSource, project, displayName string
	var port int

	if baseID != "" {
		store, err := launcher.NewStore()
		if err != nil {
			return err
		}
		base, err := store.Get(baseID)
		if err != nil {
			return fmt.Errorf("база не найдена: %w", err)
		}
		dsn = base.DB
		sqlitePath = base.DBPath
		dbType = base.DBType
		port = base.Port
		configSource = base.ConfigSource
		project = base.Path
		displayName = base.Name
		if svcName == "" {
			svcName = "onebase-" + slugify(base.Name)
		}
	} else {
		dsn, _ = cmd.Flags().GetString("db")
		sqlitePath, _ = cmd.Flags().GetString("sqlite")
		port, _ = cmd.Flags().GetInt("port")
		configSource, _ = cmd.Flags().GetString("config-source")
		project, _ = cmd.Flags().GetString("project")
		displayName = svcName
		switch {
		case dsn == "" && sqlitePath == "":
			return fmt.Errorf("укажите --id, --db или --sqlite")
		case dsn != "" && sqlitePath != "":
			return fmt.Errorf("--db и --sqlite взаимоисключающи; укажите только один")
		case sqlitePath != "":
			dbType = "sqlite"
		}
		if svcName == "" {
			svcName = "onebase"
		}
		if displayName == "" {
			displayName = svcName
		}
	}
	if dbType == "" && dsn == "" {
		dbType = "sqlite"
	}
	if dbType == "sqlite" && sqlitePath == "" {
		return fmt.Errorf("для SQLite-базы укажите путь к файлу БД (--sqlite или db_path в ibases)")
	}
	if configSource == "" {
		configSource = "database"
	}
	if configSource != "file" && configSource != "database" {
		return fmt.Errorf("--config-source должен быть file или database")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("порт должен быть в диапазоне 1..65535")
	}
	if !validServiceName(svcName) {
		return fmt.Errorf("недопустимое имя сервиса %q", svcName)
	}
	if dbType == "sqlite" {
		var err error
		sqlitePath, err = filepath.Abs(sqlitePath)
		if err != nil {
			return fmt.Errorf("абсолютный путь SQLite: %w", err)
		}
	}
	if configSource == "file" {
		if project == "" {
			project = "."
		}
		var err error
		project, err = filepath.Abs(project)
		if err != nil {
			return fmt.Errorf("абсолютный путь проекта: %w", err)
		}
		info, err := os.Stat(project)
		if err != nil {
			return fmt.Errorf("каталог проекта недоступен: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("путь проекта не является каталогом: %s", project)
		}
	}

	exe, err := os.Executable()
	if err != nil {
		exe = "onebase"
	}
	exe, _ = filepath.Abs(exe)

	watch, _ := cmd.Flags().GetBool("watch")
	switch runtime.GOOS {
	case "linux":
		return installSystemd(exe, svcName, displayName, dsn, sqlitePath, dbType, configSource, project, port, watch, cmd, printOnly)
	case "windows":
		return installWindowsService(exe, svcName, displayName, dsn, sqlitePath, dbType, configSource, project, port, watch, printOnly)
	default:
		return fmt.Errorf("автоустановка сервиса не поддерживается на %s; используйте --print для получения конфигурации", runtime.GOOS)
	}
}

// ── systemd ───────────────────────────────────────────────────────────────────

const systemdUnitTmpl = `[Unit]
Description={{systemdQuote (printf "OneBase — %s" .DisplayName)}}
After=network.target postgresql.service
Wants=postgresql.service

[Service]
Type=simple
User={{systemdQuote .User}}
ExecStart={{systemdQuote .Exe}} run --config-source {{systemdQuote .ConfigSource}} {{if eq .DBType "sqlite"}}--sqlite {{systemdQuote .SQLitePath}}{{else}}--db {{systemdQuote .DSN}}{{end}} --port {{.Port}}{{if .Project}} --project {{systemdQuote .Project}}{{end}}{{if .Watch}} --watch{{end}}
Restart=on-failure
RestartSec=5s
StandardOutput=journal
StandardError=journal
SyslogIdentifier={{systemdQuote .SvcName}}
Environment={{systemdQuote (printf "HOME=%s" .Home)}}

[Install]
WantedBy=multi-user.target
`

type systemdData struct {
	DisplayName  string
	User         string
	Home         string
	Exe          string
	SvcName      string
	DSN          string
	SQLitePath   string
	DBType       string
	ConfigSource string
	Project      string
	Port         int
	Watch        bool
}

func validServiceName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("_.@-", r) {
			continue
		}
		return false
	}
	return true
}

func systemdQuote(s string) (string, error) {
	if strings.ContainsRune(s, 0) || strings.ContainsAny(s, "\r\n") {
		return "", fmt.Errorf("systemd argument contains a control character")
	}
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "%", "%%")
	return `"` + s + `"`, nil
}

func installSystemd(exe, svcName, displayName, dsn, sqlitePath, dbType, configSource, proj string, port int, watch bool, cmd *cobra.Command, printOnly bool) error {
	user, _ := cmd.Flags().GetString("user")
	if user == "" {
		user = os.Getenv("USER")
		if user == "" {
			user = "onebase"
		}
	}
	home := "/home/" + user
	if user == "root" {
		home = "/root"
	} else if user == os.Getenv("USER") {
		if currentHome, err := os.UserHomeDir(); err == nil && currentHome != "" {
			home = currentHome
		}
	}

	data := systemdData{
		DisplayName:  displayName,
		User:         user,
		Home:         home,
		Exe:          exe,
		SvcName:      svcName,
		DSN:          dsn,
		SQLitePath:   sqlitePath,
		DBType:       dbType,
		ConfigSource: configSource,
		Project:      proj,
		Port:         port,
		Watch:        watch,
	}

	tmpl := template.Must(template.New("unit").Funcs(template.FuncMap{
		"systemdQuote": systemdQuote,
	}).Parse(systemdUnitTmpl))
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return err
	}

	if printOnly {
		_, err := os.Stdout.Write(rendered.Bytes())
		return err
	}

	unitPath := fmt.Sprintf("/etc/systemd/system/%s.service", svcName)
	tmp, err := os.CreateTemp(filepath.Dir(unitPath), "."+svcName+".service.tmp-*")
	if err != nil {
		return fmt.Errorf("не удалось записать %s (запустите с sudo): %w", unitPath, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	// The ExecStart line may contain a PostgreSQL DSN with credentials.
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(rendered.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, unitPath); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(unitPath)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}

	for _, args := range [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", svcName},
		{"systemctl", "start", svcName},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}

	fmt.Printf("Сервис %s установлен и запущен.\n", svcName)
	fmt.Printf("  Статус:  systemctl status %s\n", svcName)
	fmt.Printf("  Логи:    journalctl -u %s -f\n", svcName)
	fmt.Printf("  Стоп:    systemctl stop %s\n", svcName)
	return nil
}

// ── Windows service ───────────────────────────────────────────────────────────

func installWindowsService(exe, svcName, displayName, dsn, sqlitePath, dbType, configSource, proj string, port int, watch, printOnly bool) error {
	servicePaths := []namedPath{{Label: "каталог проекта", Path: proj}}
	if dbType == "sqlite" {
		servicePaths = append(servicePaths, namedPath{Label: "файл SQLite", Path: sqlitePath})
	}
	mapped, err := findMappedNetworkPaths(servicePaths, detectMappedNetworkDrive)
	if err != nil {
		return fmt.Errorf("проверка путей Windows-сервиса: %w", err)
	}
	if len(mapped) > 0 {
		advice := mappedDriveAdvice(mapped)
		if !printOnly {
			return fmt.Errorf("%s", advice)
		}
		fmt.Fprintln(os.Stderr, "Предупреждение:", advice)
	}

	dbFlag, dbValue := "--db", dsn
	if dbType == "sqlite" {
		dbFlag, dbValue = "--sqlite", sqlitePath
	}
	serviceArgs := []string{
		"run", "--config-source", quoteWindowsCommandArg(configSource),
		dbFlag, quoteWindowsCommandArgAlways(dbValue),
		"--port", fmt.Sprint(port),
	}
	if proj != "" {
		serviceArgs = append(serviceArgs, "--project", quoteWindowsCommandArgAlways(proj))
	}
	if watch {
		serviceArgs = append(serviceArgs, "--watch")
	}

	// SCM хранит binPath как готовую командную строку. Кавычки должны окружать
	// только executable, иначе CreateProcess обрежет путь на первом пробеле.
	// Значение остаётся отдельным аргументом exec.Command — shell не участвует.
	binPath := quoteWindowsCommandArgAlways(exe) + " " + strings.Join(serviceArgs, " ")
	scCmd := strings.Join([]string{
		"sc.exe", "create", quoteWindowsCommandArg(svcName),
		"binPath=", quoteWindowsCommandArg(binPath),
		"start=", "auto",
		"DisplayName=", quoteWindowsCommandArg("OneBase — " + displayName),
	}, " ")

	if printOnly {
		fmt.Println("# Выполните от имени администратора:")
		fmt.Println(scCmd)
		fmt.Printf("sc.exe description %s %s\n", quoteWindowsCommandArg(svcName), quoteWindowsCommandArg("OneBase business platform"))
		fmt.Printf("sc.exe start %s\n", quoteWindowsCommandArg(svcName))
		return nil
	}

	out, err := exec.Command("sc.exe", "create", svcName,
		"binPath=", binPath,
		"start=", "auto",
		"DisplayName=", "OneBase — "+displayName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc.exe create: %w\n%s", err, out)
	}
	if out, err := exec.Command("sc.exe", "description", svcName, "OneBase business platform").CombinedOutput(); err != nil {
		return fmt.Errorf("sc.exe description: %w\n%s", err, out)
	}
	if out, err := exec.Command("sc.exe", "start", svcName).CombinedOutput(); err != nil {
		return fmt.Errorf("sc.exe start: %w\n%s", err, out)
	}

	fmt.Printf("Сервис %s зарегистрирован в Windows Services.\n", svcName)
	fmt.Printf("  Запуск:  sc.exe start %s\n", svcName)
	fmt.Printf("  Стоп:    sc.exe stop %s\n", svcName)
	fmt.Printf("  Удаление: onebase service uninstall --name %s\n", svcName)
	return nil
}

// ── uninstall ─────────────────────────────────────────────────────────────────

func runServiceUninstall(cmd *cobra.Command, _ []string) error {
	svcName, _ := cmd.Flags().GetString("name")
	if !validServiceName(svcName) {
		return fmt.Errorf("недопустимое имя сервиса %q", svcName)
	}
	switch runtime.GOOS {
	case "linux":
		if out, err := exec.Command("systemctl", "disable", "--now", svcName).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl disable --now %s: %w: %s", svcName, err, strings.TrimSpace(string(out)))
		}
		unitPath := fmt.Sprintf("/etc/systemd/system/%s.service", svcName)
		if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
		}
		fmt.Printf("Сервис %s удалён.\n", svcName)
	case "windows":
		if out, err := exec.Command("sc.exe", "stop", svcName).CombinedOutput(); err != nil && !strings.Contains(string(out), "1062") {
			return fmt.Errorf("sc.exe stop: %w\n%s", err, out)
		}
		out, err := exec.Command("sc.exe", "delete", svcName).CombinedOutput()
		if err != nil {
			return fmt.Errorf("sc.exe delete: %w\n%s", err, out)
		}
		fmt.Printf("Сервис %s удалён.\n", svcName)
	default:
		return fmt.Errorf("неподдерживаемая ОС: %s", runtime.GOOS)
	}
	return nil
}

func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
