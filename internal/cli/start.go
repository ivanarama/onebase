package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/ivantit66/onebase/internal/launcher"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Open the information bases launcher",
	RunE:  runStart,
}

func runStart(_ *cobra.Command, _ []string) error {
	startLog := oblog.Component("cli.start")

	// Старт — самый безопасный момент применить скачанное обновление: баз ещё
	// нет, останавливать нечего. Работает только при включённом auto_apply
	// (план 92); после подмены перезапускаемся уже из нового бинаря.
	if launcher.ApplyStagedOnStart() {
		if err := launcher.RestartSelf(); err != nil {
			startLog.Error("обновление применено, но перезапуск не удался", "err", err)
		} else {
			return nil
		}
	}

	store, err := launcher.NewStore()
	if err != nil {
		return fmt.Errorf("start: store: %w", err)
	}

	runner := launcher.NewRunner()

	// Базы, работавшие до перезапуска ради обновления, поднимаем обратно.
	launcher.ResumeAfterUpdate(store, runner)

	srv, err := launcher.NewServer(store, runner)
	if err != nil {
		return fmt.Errorf("start: server: %w", err)
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			startLog.Error("launcher server failed", "err", err)
		}
	}()

	// OpenWindow blocks until the window/browser is closed or /quit is called.
	// For the webview build it MUST run on the main goroutine (Win32 requirement).
	// srv здесь ещё и CloseCoordinator: окно спрашивает у него, что делать с
	// работающими базами при закрытии крестиком (см. closepolicy.go).
	_ = launcher.OpenWindow(srv.URL(), "onebase — Информационные базы", srv.Done(), srv)

	// Window closed — shut down server and force exit after a short grace period
	// for lingering goroutines/threads.
	srv.Close()
	go func() {
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}()
	os.Exit(0)
	return nil
}
