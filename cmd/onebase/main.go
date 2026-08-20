package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/cli"
	"github.com/ivantit66/onebase/internal/fsmode"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/selfupdate"
)

func main() {
	oblog.ConfigureDefault()
	writeStartupLog()

	if len(os.Args) == 1 {
		reexec()
		return
	}
	if !isBinaryVersionProbeInvocation(os.Args[1:]) && !binaryWriterCommand(os.Args[1:]) {
		consumer, err := selfupdate.AcquireBinaryConsumerLeaseIfWritable()
		if errors.Is(err, selfupdate.ErrPendingBinaryTransaction) && commandName(os.Args[1:]) == "update" {
			_ = os.Setenv(selfupdate.EnvBinaryPendingEntry, "1")
			err = nil
		}
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "onebase: binary package is unavailable during update recovery: %v\n", err)
			os.Exit(1)
		}
		if consumer != nil {
			defer func() {
				if err := consumer.Release(); err != nil {
					oblog.Component("cli").Warn("binary consumer lease release failed", "err", err)
				}
			}()
		}
	}

	cli.Execute()
}

func isBinaryVersionProbeInvocation(args []string) bool {
	// Version inspection must also work while an update transaction is pending;
	// it neither consumes nor mutates the installed binary generation.
	versionFlag := false
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--version" || arg == "-v" {
			versionFlag = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg == "version"
	}
	return versionFlag
}

func binaryWriterCommand(args []string) bool {
	name := commandName(args)
	// The root command defaults to runStart when only persistent flags are
	// present (for example --no-gui or --allow-newer-schema). Treat that form as
	// the same binary-writer path as an explicit `start`; otherwise main holds a
	// consumer lease and runStart immediately deadlocks on its own writer lease.
	return name == "" || name == "start"
}

func commandName(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return ""
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
