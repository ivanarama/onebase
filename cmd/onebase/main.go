package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/cli"
	"github.com/ivantit66/onebase/internal/fsmode"
	oblog "github.com/ivantit66/onebase/internal/logging"
)

func main() {
	oblog.ConfigureDefault()
	writeStartupLog()

	if len(os.Args) == 1 {
		reexec()
		return
	}

	cli.Execute()
}

func writeStartupLog() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".onebase")
	// Каталог может уже быть или быть недоступен — тогда ниже упадёт
	// сама запись, с понятной причиной и путём.
	_ = os.MkdirAll(dir, fsmode.Dir)
	// SecretFile: диагностический след запуска пишется рядом с реестром баз, в
	// каталоге одного пользователя, и содержит аргументы командной строки
	// (пусть и с замазанным DSN — см. RedactArgs).
	f, err := os.OpenFile(filepath.Join(dir, "startup.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, fsmode.SecretFile)
	if err != nil {
		return
	}
	defer oblog.CloseQuiet("cli", "файл", f)
	exe, _ := os.Executable()
	// Диагностический след запуска: если он не записался, сообщить об этом
	// всё равно некуда — журнал ещё не поднят.
	_, _ = fmt.Fprintf(f, "%s  exe=%s  args=%s\n",
		time.Now().Format("2006-01-02 15:04:05"),
		exe,
		strings.Join(oblog.RedactArgs(os.Args[1:]), " "))
}
