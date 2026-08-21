package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Ошибка исполнения не должна тащить за собой справку по флагам.
//
// Повод — #1067: `onebase run` падал на миграции, cobra печатала два десятка
// строк usage, и лаунчер показывал в окне хвост лога, где от причины оставалась
// половина строки. Проверяем через rootCmd.Execute (argv → диспетчер cobra):
// признак SilenceUsage выставляется в PersistentPreRunE, вызов RunE напрямую
// этого пути не проходит.
func executeRootArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	// Признак ставится на исполняемой команде, поэтому соседний тест, дёрнувший
	// PersistentPreRunE прямо с rootCmd, оставляет его включённым на весь
	// процесс прогона. В отдельном процессе CLI это безразлично, здесь —
	// вопрос порядка тестов, поэтому исходное состояние восстанавливается.
	prevSilence := rootCmd.SilenceUsage
	rootCmd.SilenceUsage = false
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SilenceUsage = prevSilence
	})
	err := rootCmd.Execute()
	return out.String(), err
}

// addFailingCommand регистрирует команду, которая заведомо падает в RunE.
// Настоящие команды для этого не годятся: они либо ходят в БД, либо поднимают
// сервер, а проверяется здесь общий для всех путь вывода ошибки.
func addFailingCommand(t *testing.T) *cobra.Command {
	t.Helper()
	failing := &cobra.Command{
		Use: "failing-test-command",
		RunE: func(*cobra.Command, []string) error {
			return errors.New("подготовка базы не удалась")
		},
	}
	failing.Flags().String("вымышленный", "", "флаг ради непустой справки")
	rootCmd.AddCommand(failing)
	t.Cleanup(func() { rootCmd.RemoveCommand(failing) })
	return failing
}

func TestRunErrorDoesNotPrintFlagUsage(t *testing.T) {
	addFailingCommand(t)
	out, err := executeRootArgs(t, "failing-test-command")
	if err == nil {
		t.Fatal("команда обязана вернуть ошибку — иначе тест ничего не проверяет")
	}
	if strings.Contains(out, "Usage:") || strings.Contains(out, "Flags:") {
		t.Errorf("справка по флагам вытесняет причину отказа:\n%s", out)
	}
}

// Обратная сторона: ошибка ВЫЗОВА (неизвестный флаг) — ровно тот случай, когда
// список флагов и нужен. Если бы SilenceUsage выставлялся у rootCmd целиком,
// подсказка пропала бы и здесь.
func TestUnknownFlagStillPrintsUsage(t *testing.T) {
	addFailingCommand(t)
	out, err := executeRootArgs(t, "failing-test-command", "--такого-флага-нет")
	if err == nil {
		t.Fatal("неизвестный флаг принят молча")
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("на неизвестный флаг справка обязана печататься:\n%s", out)
	}
}

// Тот же контракт у настоящей команды: `run` — та самая, с которой начался
// #1067. Ошибку даёт несуществующий --id, до открытия порта дело не доходит.
func TestRunCommandErrorHasNoUsage(t *testing.T) {
	out, err := executeRootArgs(t, "run", "--id", "нет-такой-базы", "--no-gui")
	if err == nil {
		t.Fatal("запуск несуществующей базы обязан завершиться ошибкой")
	}
	if strings.Contains(out, "Usage:") || strings.Contains(out, "Global Flags:") {
		t.Errorf("`onebase run` печатает справку вместо причины:\n%s", out)
	}
}
