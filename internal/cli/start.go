package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ivantit66/onebase/internal/launcher"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/selfupdate"
	"github.com/spf13/cobra"
)

// launcherURLPrefix — стабильный префикс строки с адресом лаунчера. По нему
// smoke-гейт CI находит адрес в выводе процесса (план 122A).
const launcherURLPrefix = "Лаунчер доступен по адресу: "

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Open the information bases launcher",
	RunE:  runStart,
}

func runStart(_ *cobra.Command, _ []string) error {
	startLog := oblog.Component("cli.start")
	instance, err := launcher.AcquireLauncherInstance(os.Getenv(launcher.RestartWaitEnv) == "1")
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}
	defer func() {
		if err := instance.Release(); err != nil {
			startLog.Warn("launcher instance lock release failed", "err", err)
		}
	}()
	consumer, consumerErr := selfupdate.AcquireBinaryConsumerLeaseIfWritable()
	pendingAtEntry := errors.Is(consumerErr, selfupdate.ErrPendingBinaryTransaction)
	if errors.Is(consumerErr, selfupdate.ErrConsumerGenerationChanged) {
		if restartErr := launcher.RestartSelf(); restartErr != nil {
			return fmt.Errorf("start: restart after concurrent update: %w", restartErr)
		}
		return nil
	}
	if consumerErr != nil && !pendingAtEntry {
		return fmt.Errorf("start: acquire binary consumer lease: %w", consumerErr)
	}
	if consumer != nil {
		defer func() {
			if err := consumer.Release(); err != nil {
				startLog.Warn("binary consumer lease release failed", "err", err)
			}
		}()
	}

	store, err := launcher.NewStore()
	if err != nil {
		return fmt.Errorf("start: store: %w", err)
	}

	runner := launcher.NewRunner()

	// Базы, работавшие до перезапуска ради обновления, поднимаем обратно.
	if err := launcher.ResumeAfterUpdate(store, runner); err != nil {
		if errors.Is(err, launcher.ErrBinaryRecoveryRestartRequired) {
			if restartErr := launcher.RestartSelf(); restartErr != nil {
				return fmt.Errorf("start: restart after update recovery: %w", restartErr)
			}
			return nil
		}
		return fmt.Errorf("start: update recovery: %w", err)
	}
	if pendingAtEntry {
		// Another updater may have settled the marker between our entry check and
		// ResumeAfterUpdate. This process was loaded before that handoff, so it
		// must never continue even when Resume itself observed a clean target.
		if restartErr := launcher.RestartSelf(); restartErr != nil {
			return fmt.Errorf("start: restart after concurrent recovery: %w", restartErr)
		}
		return nil
	}

	// Автообновление безопасно только после recovery и проверки, что ни одна
	// база не пережила прошлый launcher в background-режиме.
	if launcher.ApplyStagedOnStart(store, runner) {
		if err := launcher.RestartSelf(); err != nil {
			return fmt.Errorf("start: update applied but restart failed: %w", err)
		}
		return nil
	}

	srv, err := launcher.NewServer(store, runner)
	if err != nil {
		return fmt.Errorf("start: server: %w", err)
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			startLog.Error("launcher server failed", "err", err)
		}
	}()

	// Адрес печатается всегда, а не только когда окно не открылось.
	//
	// Порт лаунчера динамический (127.0.0.1:0), и до этой строки узнать его
	// было неоткуда: если браузер не нашёлся или открылся не туда, человек
	// видел «нажал — ничего не произошло» и не мог зайти руками. Заодно это
	// единственный способ для smoke-гейта (план 122A) понять, куда стучаться:
	// префикс строки — часть контракта, его разбирает CI.
	outf("%s%s\n", launcherURLPrefix, srv.EntryURL())

	// OpenWindow blocks until the window/browser is closed or /quit is called.
	// For the webview build it MUST run on the main goroutine (Win32 requirement).
	// srv здесь ещё и CloseCoordinator: окно спрашивает у него, что делать с
	// работающими базами при закрытии крестиком (см. closepolicy.go).
	_ = launcher.OpenWindow(srv.EntryURL(), "onebase — Информационные базы", srv.Done(), srv)

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
