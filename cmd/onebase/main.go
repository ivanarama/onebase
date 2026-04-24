package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/cli"
)

func main() {
	writeStartupLog()
	cli.Execute()
}

// writeStartupLog записывает факт запуска в ~/.onebase/startup.log.
// Помогает диагностировать проблемы при запуске без консоли (-H windowsgui).
func writeStartupLog() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".onebase")
	os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "startup.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	exe, _ := os.Executable()
	fmt.Fprintf(f, "%s  exe=%s  args=%s\n",
		time.Now().Format("2006-01-02 15:04:05"),
		exe,
		strings.Join(os.Args[1:], " "))
}
