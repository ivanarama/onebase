package launcher

import (
	"bytes"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"

	oblog "github.com/ivantit66/onebase/internal/logging"
)

func openPathLog() *slog.Logger {
	return oblog.Component("launcher.openpath")
}

// OpenPath opens a directory or file in the OS file explorer / default app.
func OpenPath(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", path) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	case "darwin":
		cmd = exec.Command("open", path) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	default:
		cmd = exec.Command("xdg-open", path) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	}
	return cmd.Start()
}

// BrowseDir opens a native folder picker dialog and returns the selected path.
// initialPath sets the starting directory. Returns ("", nil) if the user cancelled.
func BrowseDir(title, initialPath string) (string, error) {
	switch runtime.GOOS {
	case "windows":
		initDir := ""
		if initialPath != "" {
			initDir = "$d.SelectedPath = '" + psEscape(initialPath) + "'\n"
		}
		script := `Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form
$owner.ShowInTaskbar = $false
$owner.WindowState = 'Minimized'
$owner.Opacity = 0
$owner.TopMost = $true
$owner.Show()
$owner.Activate()
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = '` + psEscape(title) + `'
$d.ShowNewFolderButton = $true
` + initDir + `$r = $d.ShowDialog($owner)
$owner.Close()
if ($r -eq 'OK') { Write-Output $d.SelectedPath }`
		out, err := runPowerShell(script)
		return strings.TrimSpace(out), err
	case "darwin":
		arg := `choose folder with prompt "` + title + `"`
		if initialPath != "" {
			arg += ` default location POSIX file "` + initialPath + `"`
		}
		out, err := exec.Command("osascript", "-e", arg).Output() //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
		if err != nil {
			return "", nil
		}
		p := strings.TrimSpace(string(out))
		p = strings.TrimPrefix(p, "alias ")
		return p, nil
	default:
		args := []string{"--file-selection", "--directory", "--title=" + title}
		if initialPath != "" {
			args = append(args, "--filename="+initialPath)
		}
		out, err := exec.Command("zenity", args...).Output() //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
		if err != nil {
			return "", nil
		}
		return strings.TrimSpace(string(out)), nil
	}
}

// BrowseFile opens a native file picker dialog and returns the selected path.
// filter is a Windows-style filter string, e.g. "SQLite (*.db)|*.db".
// Returns ("", nil) if the user cancelled.
func BrowseFile(title, filter string) (string, error) {
	switch runtime.GOOS {
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form
$owner.ShowInTaskbar = $false
$owner.WindowState = 'Minimized'
$owner.Opacity = 0
$owner.TopMost = $true
$owner.Show()
$owner.Activate()
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = '` + psEscape(title) + `'
$d.Filter = '` + psEscape(filter) + `'
$d.CheckFileExists = $false
$r = $d.ShowDialog($owner)
$owner.Close()
if ($r -eq 'OK') { Write-Output $d.FileName }`
		out, err := runPowerShell(script)
		return strings.TrimSpace(out), err
	default:
		out, err := exec.Command("zenity", "--file-selection", "--title="+title).Output() //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
		if err != nil {
			return "", nil
		}
		return strings.TrimSpace(string(out)), nil
	}
}

// psEscape экранирует значение для вставки в одинарные кавычки PowerShell.
// Внутри '...' единственный спец-символ — одинарная кавычка (удваивается).
func psEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func runPowerShell(script string) (string, error) {
	// -Sta обязателен для WinForms (FolderBrowserDialog/OpenFileDialog требуют STA-апартмент).
	// Без него на Server 2016/2019/2022 ShowDialog может молча зависнуть или вернуть ошибку.
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Sta", "-WindowStyle", "Hidden", "-Command", script) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	noWindow(cmd)                                                                                                            // CREATE_NO_WINDOW: suppresses the brief flash before -WindowStyle kicks in
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Пишем причину в server log — пользователь видит «ничего», но при репорте
		// можно понять что именно сломалось (AppLocker, отсутствие WinForms на Server Core, etc).
		if s := strings.TrimSpace(stderr.String()); s != "" {
			openPathLog().Warn("powershell dialog failed", "err", err, "stderr", s)
		} else {
			openPathLog().Warn("powershell dialog failed", "err", err)
		}
		return "", nil // user cancelled = exit code != 0
	}
	return stdout.String(), nil
}
