package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/launcher"
	"github.com/ivantit66/onebase/internal/selfupdate"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Offline-обновление бинаря из архива/файла с проверкой и откатом",
	Long: `Обновляет установленный бинарь onebase из локального архива или .exe — для
offline-серверов, куда обновление приносят на флешке. Останавливает сервис,
подменяет бинарь (сохраняя старый рядом), запускает сервис и опрашивает /healthz.
Если новый бинарь не отвечает за --timeout — откатывает старый и перезапускает.`,
	Example: `  onebase update --from D:\flash\onebase-v0.9.1.zip --sha256 <hex> --id my-base
  onebase update --from ./onebase.exe --service onebase-docflow --port 8080 --sha256 <hex>`,
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().String("from", "", "путь к обновлению: .zip (внутри ищется onebase[.exe]) или сам бинарь (обязателен)")
	updateCmd.Flags().String("sha256", "", "ожидаемая SHA256 файла обновления (.zip/.exe, 64 hex; обязательна)")
	updateCmd.Flags().String("service", "", "имя системного сервиса (иначе выводится из --id)")
	updateCmd.Flags().String("id", "", "ID базы из реестра ibases (даёт имя сервиса и порт)")
	updateCmd.Flags().String("target", "", "путь к заменяемому бинарю onebase (по умолчанию — текущий исполняемый файл)")
	updateCmd.Flags().String("healthz-url", "", "URL readiness-пробы (по умолчанию http://127.0.0.1:<port>/healthz)")
	updateCmd.Flags().Int("port", 8080, "порт для /healthz (переопределяется базой при --id)")
	updateCmd.Flags().Duration("timeout", 30*time.Second, "сколько ждать 200 от /healthz после запуска")
	_ = updateCmd.MarkFlagRequired("from")
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	from, _ := cmd.Flags().GetString("from")
	sha, _ := cmd.Flags().GetString("sha256")
	timeout, _ := cmd.Flags().GetDuration("timeout")

	svcName, healthzURL, target, err := resolveUpdateTarget(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(sha) == "" {
		return fmt.Errorf("укажите --sha256: обновление без проверки контрольной суммы запрещено")
	}

	// 1. Проверить артефакт обновления, затем извлечь/подготовить новый бинарь
	// во временный каталог.
	if err := selfupdate.VerifySHA256(from, sha); err != nil {
		return err
	}
	outln("SHA256 файла обновления совпала.")
	stageDir, err := os.MkdirTemp("", "onebase-update-*")
	if err != nil {
		return err
	}
	defer removeTemp(stageDir)

	newBin, err := selfupdate.StageBinary(from, stageDir)
	if err != nil {
		return err
	}
	expectedVersion, err := binaryVersion(newBin)
	if err != nil {
		return err
	}

	// 2. Остановить сервис — освобождаем бинарь.
	outf("Останавливаю сервис %s ...\n", svcName)
	if err := stopService(svcName, timeout); err != nil {
		return err
	}

	// 3. Подменить бинарь, сохранив старый рядом (.old) для отката.
	backup, err := selfupdate.SwapBinary(target, newBin)
	if err != nil {
		// Подмена не удалась — старый бинарь на месте, просто поднимаем сервис.
		_ = startService(svcName, timeout)
		return err
	}
	outf("Бинарь заменён (старый сохранён: %s).\n", filepath.Base(backup))

	// 4. Запустить сервис и убедиться, что новый бинарь отвечает.
	outf("Запускаю сервис и жду %s (%s) ...\n", healthzURL, timeout)
	startErr := startService(svcName, timeout)
	if startErr == nil {
		startErr = selfupdate.PollHealthzVersion(context.Background(), healthzURL, expectedVersion, timeout, time.Second)
	}
	if startErr != nil {
		// 5. Откат: остановить, вернуть старый бинарь, снова запустить.
		fmt.Fprintf(os.Stderr, "Новый бинарь не поднялся (%v) — откатываюсь.\n", startErr)
		_ = stopService(svcName, timeout)
		if rbErr := selfupdate.Rollback(target, backup); rbErr != nil {
			return fmt.Errorf("КРИТИЧНО: откат не удался: %w (исходная ошибка: %v)", rbErr, startErr)
		}
		if rsErr := startService(svcName, timeout); rsErr != nil {
			return fmt.Errorf("откат выполнен, но сервис не стартовал: %w", rsErr)
		}
		return fmt.Errorf("обновление откачено, работает прежний бинарь: %w", startErr)
	}

	// 6. Успех — прибираем резервную копию (не критично, если не вышло).
	_ = os.Remove(backup)
	outf("Готово: обновление применено, %s отвечает 200.\n", healthzURL)
	return nil
}

func binaryVersion(path string) (string, error) {
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("не удалось определить версию нового бинаря: %w: %s", err, strings.TrimSpace(string(out)))
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return "", fmt.Errorf("новый бинарь вернул пустую версию")
	}
	return fields[len(fields)-1], nil
}

// resolveUpdateTarget вычисляет имя сервиса, URL пробы и путь к заменяемому бинарю
// из флагов (--service/--id, --healthz-url/--port, --target).
func resolveUpdateTarget(cmd *cobra.Command) (svcName, healthzURL, target string, err error) {
	svcName, _ = cmd.Flags().GetString("service")
	id, _ := cmd.Flags().GetString("id")
	port, _ := cmd.Flags().GetInt("port")

	if id != "" {
		store, sErr := launcher.NewStore()
		if sErr != nil {
			return "", "", "", sErr
		}
		base, gErr := store.Get(id)
		if gErr != nil {
			return "", "", "", fmt.Errorf("база не найдена: %w", gErr)
		}
		if svcName == "" {
			svcName = "onebase-" + slugify(base.Name)
		}
		if !cmd.Flags().Changed("port") && base.Port != 0 {
			port = base.Port
		}
	}
	if svcName == "" {
		return "", "", "", fmt.Errorf("укажите --service <имя> или --id <база> для определения сервиса")
	}

	healthzURL, _ = cmd.Flags().GetString("healthz-url")
	if healthzURL == "" {
		healthzURL = fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	}

	target, _ = cmd.Flags().GetString("target")
	if target == "" {
		exe, eErr := os.Executable()
		if eErr != nil {
			return "", "", "", fmt.Errorf("не удалось определить текущий бинарь: %w (укажите --target)", eErr)
		}
		target = exe
	}
	target, _ = filepath.Abs(target)
	return svcName, healthzURL, target, nil
}

// stopService останавливает системный сервис и ждёт его полной остановки.
func stopService(name string, timeout time.Duration) error {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("sc.exe", "stop", name).CombinedOutput() //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
		// 1062 = «сервис не запущен» — не ошибка для нашей цели.
		if err != nil && !strings.Contains(string(out), "1062") {
			return fmt.Errorf("sc stop %s: %w\n%s", name, err, out)
		}
		return waitWindowsState(name, "STOPPED", timeout)
	case "linux":
		if out, err := exec.Command("systemctl", "stop", name).CombinedOutput(); err != nil { //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
			return fmt.Errorf("systemctl stop %s: %w\n%s", name, err, out)
		}
		return nil
	default:
		return fmt.Errorf("обновление сервиса не поддерживается на %s", runtime.GOOS)
	}
}

// startService запускает системный сервис и ждёт перехода в рабочее состояние.
func startService(name string, timeout time.Duration) error {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("sc.exe", "start", name).CombinedOutput() //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
		// 1056 = «сервис уже запущен».
		if err != nil && !strings.Contains(string(out), "1056") {
			return fmt.Errorf("sc start %s: %w\n%s", name, err, out)
		}
		return waitWindowsState(name, "RUNNING", timeout)
	case "linux":
		if out, err := exec.Command("systemctl", "start", name).CombinedOutput(); err != nil { //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
			return fmt.Errorf("systemctl start %s: %w\n%s", name, err, out)
		}
		return nil
	default:
		return fmt.Errorf("обновление сервиса не поддерживается на %s", runtime.GOOS)
	}
}

// waitWindowsState опрашивает `sc.exe query` до появления нужного состояния
// (STOPPED/RUNNING) или до истечения timeout.
func waitWindowsState(name, state string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		out, _ := exec.Command("sc.exe", "query", name).CombinedOutput() //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
		if strings.Contains(string(out), state) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("сервис %s не перешёл в %s за %s", name, state, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
