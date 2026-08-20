package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/launcher"
	"github.com/ivantit66/onebase/internal/selfupdate"
	"github.com/ivantit66/onebase/internal/version"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Обновление платформы: из сети по каналу или офлайн из архива",
	Long: `Обновляет установленный бинарь onebase.

По умолчанию берёт сборку из GitHub-релизов выбранного канала:
  build  — сборки из main (build-NNN), выходят по нескольку раз в день;
  stable — только теги vX.Y.Z.

Для offline-серверов остаётся прежний путь: --from <файл> с обязательной
--sha256 (обновление приносят на флешке).

Если указана системная служба (--service/--id), она останавливается на время
подмены и поднимается обратно с проверкой /healthz; без неё меняется только
файл бинаря — так обновляется десктопная установка с лаунчером.`,
	Example: `  onebase update --check
  onebase update --channel stable
  onebase update --id my-base
  onebase update --from D:\flash\onebase-v0.9.1.zip --sha256 <hex> --id my-base
  onebase update --rollback`,
	RunE: runUpdate,
}

func init() {
	registerUpdateFlags(updateCmd)
}

// registerUpdateFlags объявляет флаги команды. Отдельной функцией, чтобы тесты
// собирали свой экземпляр команды и не переиспользовали глобальный updateCmd с
// его состоянием флагов.
func registerUpdateFlags(updateCmd *cobra.Command) {
	updateCmd.Flags().Bool("check", false, "только проверить наличие обновления, ничего не менять")
	updateCmd.Flags().Bool("json", false, "вывод проверки в JSON (для мониторинга и планировщика)")
	updateCmd.Flags().Bool("download", false, "скачать обновление, но не применять")
	updateCmd.Flags().Bool("rollback", false, "вернуть предыдущую версию платформы")
	updateCmd.Flags().String("channel", "", "канал обновлений: build или stable (запоминается)")
	updateCmd.Flags().String("repo", "", "репозиторий-источник в формате владелец/имя")
	updateCmd.Flags().String("from", "", "офлайн-обновление: путь к .zip (внутри ищется onebase[.exe]) или сам бинарь")
	updateCmd.Flags().String("sha256", "", "ожидаемая SHA256 файла из --from (64 hex; обязательна для офлайн-пути)")
	updateCmd.Flags().String("service", "", "имя системной службы (иначе выводится из --id)")
	updateCmd.Flags().String("id", "", "ID базы из реестра ibases (даёт имя службы и порт)")
	updateCmd.Flags().String("target", "", "путь к заменяемому бинарю onebase (по умолчанию — текущий исполняемый файл)")
	updateCmd.Flags().String("healthz-url", "", "URL readiness-пробы (по умолчанию http://127.0.0.1:<port>/healthz)")
	updateCmd.Flags().Int("port", 8080, "порт для /healthz (переопределяется базой при --id)")
	updateCmd.Flags().Duration("timeout", 30*time.Second, "сколько ждать 200 от /healthz после запуска")
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	check, _ := cmd.Flags().GetBool("check")
	rollback, _ := cmd.Flags().GetBool("rollback")
	from, _ := cmd.Flags().GetString("from")

	switch {
	case rollback:
		return runUpdateRollback(cmd)
	case check:
		return runUpdateCheck(cmd)
	case strings.TrimSpace(from) != "":
		return runUpdateOffline(cmd)
	default:
		return runUpdateNetwork(cmd)
	}
}

// updateContext собирает всё, что нужно знать обеим веткам обновления: где
// лежат бинари, какой канал разрешён и не запрещено ли обновление вовсе.
type updateContext struct {
	policy    selfupdate.Policy
	state     selfupdate.State
	channel   selfupdate.Channel
	repo      string
	targetDir string
}

func newUpdateContext(cmd *cobra.Command) (updateContext, error) {
	var uc updateContext

	targetDir, err := updateTargetDir(cmd)
	if err != nil {
		return uc, err
	}
	uc.targetDir = targetDir
	uc.policy = selfupdate.LoadPolicy(targetDir)

	st, err := selfupdate.LoadState()
	if err != nil {
		// Битое состояние не мешает работе: оно будет перезаписано.
		outf("Предупреждение: %v\n", err)
	}
	uc.state = st

	flagChannel, _ := cmd.Flags().GetString("channel")
	flagChannel = strings.ToLower(strings.TrimSpace(flagChannel))
	switch {
	case flagChannel == "":
		uc.channel = uc.policy.ChannelOr(st.ChannelOrDefault())
	case uc.policy.ChannelLocked() && selfupdate.Channel(flagChannel) != uc.policy.ChannelOr(""):
		return uc, fmt.Errorf("канал зафиксирован политикой: %s (см. %s)",
			uc.policy.ChannelOr(""), filepath.Join(targetDir, selfupdate.PolicyFileName))
	case flagChannel == string(selfupdate.ChannelBuild), flagChannel == string(selfupdate.ChannelStable):
		uc.channel = selfupdate.Channel(flagChannel)
	default:
		return uc, fmt.Errorf("неизвестный канал %q (ожидались build или stable)", flagChannel)
	}

	flagRepo, _ := cmd.Flags().GetString("repo")
	uc.repo = uc.policy.RepoOr(flagRepo)
	return uc, nil
}

// updateTargetDir возвращает каталог, в котором лежат бинари платформы.
func updateTargetDir(cmd *cobra.Command) (string, error) {
	if target, _ := cmd.Flags().GetString("target"); target != "" {
		abs, err := filepath.Abs(target)
		if err != nil {
			return "", err
		}
		// --target исторически указывает на файл, но заменяем мы весь набор
		// бинарей пакета, поэтому дальше работаем с каталогом.
		if info, err := os.Stat(abs); err == nil && info.IsDir() {
			return abs, nil
		}
		return filepath.Dir(abs), nil
	}
	return selfupdate.BinaryDir()
}

// updateTargetRefusal превращает отказ проверки каталога в совет, который можно
// выполнить. Раньше все причины сливались в одну строку «не поддерживает
// безопасное самообновление», и пользователь общей установки шёл искать
// администратора — хотя администратор здесь не при чём: правило смотрит на
// расположение каталога.
func updateTargetRefusal(action, dir string, err error) error {
	switch {
	case errors.Is(err, selfupdate.ErrTargetShared):
		return fmt.Errorf("%s недоступно: платформа установлена в общий каталог %s, а самообновление работает только из личной установки"+
			" (на Windows — каталог внутри профиля пользователя). Запуск от администратора этого не меняет."+
			" Обновите вручную, распаковав архив выпуска поверх, либо переустановите платформу в свой профиль", action, dir)
	case errors.Is(err, selfupdate.ErrTargetNotWritable):
		return fmt.Errorf("%s недоступно: нет прав на запись в каталог платформы %s —"+
			" выполните команду под учётной записью, которой принадлежит установка", action, dir)
	default:
		return fmt.Errorf("установка %s не поддерживает безопасное изменение версии: %w", dir, err)
	}
}

func runUpdateCheck(cmd *cobra.Command) error {
	asJSON, _ := cmd.Flags().GetBool("json")
	uc, err := newUpdateContext(cmd)
	if err != nil {
		return err
	}
	if !uc.policy.CheckAllowed() {
		return fmt.Errorf("проверка обновлений запрещена политикой (см. %s)", filepath.Join(uc.targetDir, selfupdate.PolicyFileName))
	}

	st, err := selfupdate.Check(context.Background(), selfupdate.Options{Repo: uc.repo, Channel: uc.channel})
	if err != nil {
		return err
	}
	current := binaryVersionOr(uc.targetDir, version.String())
	available := st.UpdateAvailable(current)

	if asJSON {
		out := map[string]any{
			"current":          current,
			"channel":          string(uc.channel),
			"repo":             uc.repo,
			"update_available": available,
			// Ложь здесь объясняет, почему update_available всегда false:
			// версию такого бинаря не с чем сравнивать. Мониторингу это надо
			// отличать от «стоит свежая версия».
			"version_comparable": selfupdate.KnownVersionScheme(current),
		}
		if st.Latest != nil {
			out["latest"] = st.Latest.Tag
			out["published_at"] = st.Latest.PublishedAt
			out["url"] = st.Latest.URL
			out["same_scheme"] = selfupdate.SameScheme(current, st.Latest.Tag)
		}
		if st.StagedReady() {
			out["staged"] = st.Staged.Tag
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	outf("Текущая версия:  %s\n", current)
	outf("Канал:           %s (%s)\n", uc.channel, uc.repo)
	if !selfupdate.KnownVersionScheme(current) {
		outln("Версия не сопоставляется с выпусками (сборка разработчика или нестандартный ярлык) —")
		outln("обновление предложено не будет. Сравнимы только build-<число> и vX.Y.Z.")
	}
	if st.Latest == nil {
		outln("В канале нет доступных релизов.")
		return nil
	}
	outf("Доступна версия: %s от %s\n", st.Latest.Tag, st.Latest.PublishedAt.Local().Format("02.01.2006 15:04"))
	switch {
	case !available:
		outln("Обновление не требуется — установлена актуальная версия.")
	case !selfupdate.SameScheme(current, st.Latest.Tag):
		outf("Канал %s предлагает %s — это переключение канала, а не более новая сборка.\n", uc.channel, st.Latest.Tag)
	default:
		outln("Доступно обновление. Применить: onebase update")
	}
	if st.StagedReady() {
		outf("Уже скачано и готово к применению: %s\n", st.Staged.Tag)
	}
	return nil
}

func runUpdateNetwork(cmd *cobra.Command) error {
	downloadOnly, _ := cmd.Flags().GetBool("download")
	uc, err := newUpdateContext(cmd)
	if err != nil {
		return err
	}
	if !uc.policy.CheckAllowed() {
		return fmt.Errorf("обновление из сети запрещено политикой (см. %s); офлайн-путь: --from <файл> --sha256 <hex>",
			filepath.Join(uc.targetDir, selfupdate.PolicyFileName))
	}
	current := binaryVersionOr(uc.targetDir, version.String())

	ctx := context.Background()
	st, err := selfupdate.Check(ctx, selfupdate.Options{Repo: uc.repo, Channel: uc.channel})
	if err != nil {
		return err
	}
	if st.Latest == nil {
		return fmt.Errorf("в канале %s нет доступных релизов", uc.channel)
	}
	if !st.UpdateAvailable(current) {
		outf("Установлена актуальная версия %s — обновление не требуется.\n", current)
		return nil
	}
	outf("Текущая версия %s, в канале %s доступна %s.\n", current, uc.channel, st.Latest.Tag)

	// Скачиваем, только если этой версии ещё нет в staging: повторный вызов
	// после `--download` не должен тянуть архив второй раз.
	staged := st.Staged
	if !st.StagedReady() || staged.Tag != st.Latest.Tag || !selfupdate.StagedFilesAvailable(*staged) {
		rel, err := selfupdate.LatestRelease(ctx, uc.repo, uc.channel)
		if err != nil {
			return err
		}
		outf("Скачиваю %s ...\n", rel.AssetName)
		s, err := selfupdate.Fetch(ctx, rel)
		if err != nil {
			return err
		}
		staged = &s
		outln("Контрольная сумма совпала, бинари распакованы.")
	} else {
		outf("Обновление %s уже скачано.\n", staged.Tag)
	}

	if downloadOnly {
		outf("Готово к применению. Применить: onebase update\n")
		return nil
	}
	return applyStaged(cmd, uc, *staged)
}

// applyStaged останавливает службу (если она указана), подменяет бинари и
// убеждается, что новая версия работает. Если нет — возвращает прежнюю.
func applyStaged(cmd *cobra.Command, uc updateContext, staged selfupdate.StagedInfo) (resultErr error) {
	timeout, _ := cmd.Flags().GetDuration("timeout")
	svcName, healthzURL, err := resolveService(cmd)
	if err != nil {
		return err
	}
	if err := selfupdate.ValidateBinaryUpdateTarget(uc.targetDir); err != nil {
		return updateTargetRefusal("обновление платформы", uc.targetDir, err)
	}
	lease, err := selfupdate.AcquireOperationLease()
	if err != nil {
		return err
	}
	serviceStopped := false
	restartServiceOnError := false
	defer func() {
		if resultErr != nil && serviceStopped && restartServiceOnError {
			if releaseErr := lease.ReleaseTargetReservation(); releaseErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("освободить lifecycle-блокировку перед запуском службы: %w", releaseErr))
				return
			}
			if restartErr := startService(svcName, timeout); restartErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("служба %s не запустилась после безопасного отказа обновления: %w", svcName, restartErr))
			}
		}
	}()
	defer func() {
		if releaseErr := lease.Release(); releaseErr != nil {
			outf("Предупреждение: блокировка обновления не освобождена: %v\n", releaseErr)
		}
	}()
	if err := lease.ReserveTarget(uc.targetDir); err != nil {
		return err
	}
	if svcName != "" {
		outf("Останавливаю службу %s ...\n", svcName)
		if err := stopService(svcName, timeout); err != nil {
			return err
		}
		serviceStopped = true
	}
	recovered, err := lease.RecoverWithResult(uc.targetDir)
	if err != nil {
		return fmt.Errorf("recover interrupted update: %w", err)
	}
	if recovered || os.Getenv(selfupdate.EnvBinaryPendingEntry) == "1" {
		return fmt.Errorf("interrupted update was recovered; restart this command from the installed binary")
	}
	restartServiceOnError = true

	latest, err := selfupdate.LoadState()
	if err != nil {
		return err
	}
	if latest.RecoveryPending() {
		return fmt.Errorf("не завершено восстановление баз после предыдущего обновления")
	}
	if !sameCLIStaged(latest.Staged, &staged) || !selfupdate.StagedFilesAvailable(staged) {
		return fmt.Errorf("скачанное обновление изменилось или больше недоступно; скачайте его заново")
	}
	// Версию для отката спрашиваем у заменяемого бинаря, а не у себя: с
	// --target обновляют чужую установку, и версия процесса CLI там ни при чём.
	prevTag := binaryVersionOr(uc.targetDir, version.String())
	if _, err := selfupdate.UpdateState(func(st *selfupdate.State) error {
		if st.RecoveryPending() || !sameCLIStaged(st.Staged, &staged) {
			return fmt.Errorf("состояние обновления изменилось; повторите операцию")
		}
		// Prev becomes truthful only after Apply has created the new rollback
		// snapshot. Persisting it before the swap can expose an older snapshot
		// under the new version label after a crash.
		return nil
	}); err != nil {
		return err
	}

	if err := lease.ApplyWithRollbackState(staged, uc.targetDir, prevTag); err != nil {
		if selfupdate.RecoveryPending(err) {
			restartServiceOnError = false
		}
		snapshotErr := lease.ValidateRollbackSnapshot(uc.targetDir)
		_, _ = selfupdate.UpdateState(func(st *selfupdate.State) error {
			if snapshotErr != nil {
				st.Prev = nil
			}
			if sameCLIStaged(st.Staged, &staged) && !selfupdate.StagedFilesAvailable(staged) {
				st.Staged = nil
			}
			return nil
		})
		return err
	}
	outf("Бинари заменены на %s.\n", staged.Tag)

	if svcName != "" {
		if err := lease.ReleaseTargetReservation(); err != nil {
			return fmt.Errorf("освободить lifecycle-блокировку перед проверочным запуском службы: %w", err)
		}
	}
	if err := verifyAfterSwap(uc.targetDir, svcName, healthzURL, staged.Tag, timeout); err != nil {
		fmt.Fprintf(os.Stderr, "Новая версия не подтвердилась (%v) — откатываюсь.\n", err)
		if svcName != "" {
			_ = stopService(svcName, timeout)
			serviceStopped = true
			if reserveErr := lease.ReserveTarget(uc.targetDir); reserveErr != nil {
				restartServiceOnError = false
				return fmt.Errorf("КРИТИЧНО: не удалось вернуть lifecycle-блокировку для отката: %w (исходная ошибка: %v)", reserveErr, err)
			}
		}
		if rbErr := lease.RollbackPrev(uc.targetDir); rbErr != nil {
			if selfupdate.RecoveryPending(rbErr) {
				restartServiceOnError = false
			}
			return fmt.Errorf("КРИТИЧНО: откат не удался: %w (исходная ошибка: %v)", rbErr, err)
		}
		_, _ = selfupdate.UpdateState(func(st *selfupdate.State) error {
			st.Prev = nil
			if sameCLIStaged(st.Staged, &staged) {
				st.Staged = nil
			}
			return nil
		})
		return fmt.Errorf("обновление откачено, работает прежняя версия: %w", err)
	}
	serviceStopped = false

	outf("Готово: платформа обновлена до %s.\n", staged.Tag)
	if svcName == "" {
		outln("Запущенные процессы продолжают работать на прежней версии — перезапустите их.")
	}
	return nil
}

// binaryVersionOr спрашивает версию установленного бинаря, возвращая def, если
// спросить не вышло (бинарь занят, не запускается, каталог чужой).
func binaryVersionOr(targetDir, def string) string {
	if got, err := selfupdate.BinaryVersion(filepath.Join(targetDir, selfupdate.BinaryName())); err == nil {
		return got
	}
	return def
}

// verifyAfterSwap подтверждает, что подменённые бинари работоспособны. Со
// службой критерий строгий — она поднялась и отвечает 200 нужной версией; без
// службы проверять некому, поэтому спрашиваем сам бинарь.
func verifyAfterSwap(targetDir, svcName, healthzURL, wantTag string, timeout time.Duration) error {
	if svcName == "" {
		got, err := selfupdate.BinaryVersion(filepath.Join(targetDir, selfupdate.BinaryName()))
		if err != nil {
			return err
		}
		if got != wantTag {
			return fmt.Errorf("установленный бинарь сообщает версию %q, ожидалась %q", got, wantTag)
		}
		return nil
	}
	outf("Запускаю службу и жду %s (%s) ...\n", healthzURL, timeout)
	if err := startService(svcName, timeout); err != nil {
		return err
	}
	return selfupdate.PollHealthzVersion(context.Background(), healthzURL, wantTag, timeout, time.Second)
}

func runUpdateRollback(cmd *cobra.Command) (resultErr error) {
	timeout, _ := cmd.Flags().GetDuration("timeout")
	uc, err := newUpdateContext(cmd)
	if err != nil {
		return err
	}
	svcName, healthzURL, err := resolveService(cmd)
	if err != nil {
		return err
	}
	if err := selfupdate.ValidateBinaryUpdateTarget(uc.targetDir); err != nil {
		return updateTargetRefusal("возврат версии", uc.targetDir, err)
	}
	lease, err := selfupdate.AcquireOperationLease()
	if err != nil {
		return err
	}
	serviceStopped := false
	restartServiceOnError := false
	defer func() {
		if resultErr != nil && serviceStopped && restartServiceOnError {
			if releaseErr := lease.ReleaseTargetReservation(); releaseErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("освободить lifecycle-блокировку перед запуском службы: %w", releaseErr))
				return
			}
			if restartErr := startService(svcName, timeout); restartErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("служба %s не запустилась после безопасного отказа отката: %w", svcName, restartErr))
			}
		}
	}()
	defer func() {
		if releaseErr := lease.Release(); releaseErr != nil {
			outf("Предупреждение: блокировка обновления не освобождена: %v\n", releaseErr)
		}
	}()
	if err := lease.ReserveTarget(uc.targetDir); err != nil {
		return err
	}
	if svcName != "" {
		outf("Останавливаю службу %s ...\n", svcName)
		if err := stopService(svcName, timeout); err != nil {
			return err
		}
		serviceStopped = true
	}
	recovered, err := lease.RecoverWithResult(uc.targetDir)
	if err != nil {
		return fmt.Errorf("recover interrupted update: %w", err)
	}
	if recovered || os.Getenv(selfupdate.EnvBinaryPendingEntry) == "1" {
		return fmt.Errorf("interrupted update was recovered; restart this command from the installed binary")
	}
	restartServiceOnError = true
	latest, err := selfupdate.LoadState()
	if err != nil {
		return err
	}
	if latest.RecoveryPending() {
		return fmt.Errorf("не завершено восстановление баз после предыдущего обновления")
	}
	if latest.Prev == nil {
		return fmt.Errorf("нет предыдущей версии для отката")
	}
	targetDir, err := selfupdate.CanonicalTargetDir(uc.targetDir)
	if err != nil {
		return err
	}
	if latest.Prev.TargetDir == "" || latest.Prev.TargetDir != targetDir {
		return fmt.Errorf("предыдущая версия сохранена для другой установки")
	}
	expectedPrevTag := latest.Prev.Tag

	if err := lease.RollbackPrev(uc.targetDir); err != nil {
		if selfupdate.RecoveryPending(err) {
			restartServiceOnError = false
		}
		return err
	}
	got, err := selfupdate.BinaryVersion(filepath.Join(uc.targetDir, selfupdate.BinaryName()))
	if err != nil {
		return err
	}
	if svcName != "" {
		if err := lease.ReleaseTargetReservation(); err != nil {
			return fmt.Errorf("освободить lifecycle-блокировку перед запуском службы: %w", err)
		}
		if err := startService(svcName, timeout); err != nil {
			return err
		}
		serviceStopped = false
		if err := selfupdate.PollHealthzVersion(context.Background(), healthzURL, got, timeout, time.Second); err != nil {
			return err
		}
	}

	if _, err := selfupdate.UpdateState(func(st *selfupdate.State) error {
		if st.Prev != nil && st.Prev.Tag == expectedPrevTag && st.Prev.TargetDir == targetDir {
			st.Prev = nil
		}
		return nil
	}); err != nil {
		outf("Предупреждение: состояние обновлений не сохранено: %v\n", err)
	}
	outf("Откат выполнен: работает версия %s.\n", got)
	return nil
}

func sameCLIStaged(a, b *selfupdate.StagedInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Tag == b.Tag && a.Dir == b.Dir && a.Verified == b.Verified &&
		a.StagedAt.Equal(b.StagedAt) && slices.Equal(a.Files, b.Files)
}

// runUpdateOffline — прежний путь для машин без интернета: обновление приносят
// файлом, контрольную сумму называет тот, кто его принёс.
func runUpdateOffline(cmd *cobra.Command) (resultErr error) {
	from, _ := cmd.Flags().GetString("from")
	sha, _ := cmd.Flags().GetString("sha256")
	timeout, _ := cmd.Flags().GetDuration("timeout")

	uc, err := newUpdateContext(cmd)
	if err != nil {
		return err
	}
	svcName, healthzURL, err := resolveService(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(sha) == "" {
		return fmt.Errorf("укажите --sha256: обновление без проверки контрольной суммы запрещено")
	}
	if err := selfupdate.ValidateBinaryUpdateTarget(uc.targetDir); err != nil {
		return updateTargetRefusal("обновление платформы", uc.targetDir, err)
	}

	// 1. Проверить артефакт обновления, затем извлечь бинари во временный каталог.
	if err := selfupdate.VerifySHA256(from, sha); err != nil {
		return err
	}
	outln("SHA256 файла обновления совпала.")
	stageDir, err := os.MkdirTemp("", "onebase-update-*")
	if err != nil {
		return err
	}
	defer removeTemp(stageDir)

	files, err := stageOffline(from, stageDir)
	if err != nil {
		return err
	}
	tag, err := selfupdate.BinaryVersion(files[selfupdate.BinaryName()])
	if err != nil {
		return err
	}
	staged := selfupdate.StagedInfo{Tag: tag, Dir: stageDir, Files: offlineNames(files), Verified: true}
	lease, err := selfupdate.AcquireOperationLease()
	if err != nil {
		return err
	}
	serviceStopped := false
	restartServiceOnError := false
	defer func() {
		if resultErr != nil && serviceStopped && restartServiceOnError {
			if releaseErr := lease.ReleaseTargetReservation(); releaseErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("освободить lifecycle-блокировку перед запуском службы: %w", releaseErr))
				return
			}
			if restartErr := startService(svcName, timeout); restartErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("служба %s не запустилась после безопасного отказа обновления: %w", svcName, restartErr))
			}
		}
	}()
	defer func() {
		if releaseErr := lease.Release(); releaseErr != nil {
			outf("Предупреждение: блокировка обновления не освобождена: %v\n", releaseErr)
		}
	}()
	if err := lease.ReserveTarget(uc.targetDir); err != nil {
		return err
	}
	if svcName != "" {
		outf("Останавливаю службу %s ...\n", svcName)
		if err := stopService(svcName, timeout); err != nil {
			return err
		}
		serviceStopped = true
	}
	recovered, err := lease.RecoverWithResult(uc.targetDir)
	if err != nil {
		return fmt.Errorf("recover interrupted update: %w", err)
	}
	if recovered || os.Getenv(selfupdate.EnvBinaryPendingEntry) == "1" {
		return fmt.Errorf("interrupted update was recovered; restart this command from the installed binary")
	}
	restartServiceOnError = true
	latest, err := selfupdate.LoadState()
	if err != nil {
		return err
	}
	if latest.RecoveryPending() {
		return fmt.Errorf("не завершено восстановление баз после предыдущего обновления")
	}
	prevTag := binaryVersionOr(uc.targetDir, version.String())

	// 2. Дальше путь общий с сетевым: остановить службу, подменить, проверить,
	// при неудаче — откатиться.
	if _, err := selfupdate.UpdateState(func(st *selfupdate.State) error {
		if st.RecoveryPending() {
			return fmt.Errorf("не завершено восстановление баз после предыдущего обновления")
		}
		// Validate recovery state without advertising a rollback snapshot that
		// Apply has not created yet.
		return nil
	}); err != nil {
		return err
	}
	if err := lease.ApplyWithRollbackState(staged, uc.targetDir, prevTag); err != nil {
		if selfupdate.RecoveryPending(err) {
			restartServiceOnError = false
		}
		if lease.ValidateRollbackSnapshot(uc.targetDir) != nil {
			_, _ = selfupdate.UpdateState(func(st *selfupdate.State) error {
				st.Prev = nil
				return nil
			})
		}
		return err
	}
	outf("Бинари заменены на %s.\n", tag)

	if svcName != "" {
		if err := lease.ReleaseTargetReservation(); err != nil {
			return fmt.Errorf("освободить lifecycle-блокировку перед проверочным запуском службы: %w", err)
		}
	}
	if err := verifyAfterSwap(uc.targetDir, svcName, healthzURL, tag, timeout); err != nil {
		fmt.Fprintf(os.Stderr, "Новая версия не подтвердилась (%v) — откатываюсь.\n", err)
		if svcName != "" {
			_ = stopService(svcName, timeout)
			serviceStopped = true
			if reserveErr := lease.ReserveTarget(uc.targetDir); reserveErr != nil {
				restartServiceOnError = false
				return fmt.Errorf("КРИТИЧНО: не удалось вернуть lifecycle-блокировку для отката: %w (исходная ошибка: %v)", reserveErr, err)
			}
		}
		if rbErr := lease.RollbackPrev(uc.targetDir); rbErr != nil {
			if selfupdate.RecoveryPending(rbErr) {
				restartServiceOnError = false
			}
			return fmt.Errorf("КРИТИЧНО: откат не удался: %w (исходная ошибка: %v)", rbErr, err)
		}
		_, _ = selfupdate.UpdateState(func(st *selfupdate.State) error {
			st.Prev = nil
			return nil
		})
		return fmt.Errorf("обновление откачено, работает прежняя версия: %w", err)
	}
	serviceStopped = false
	outf("Готово: платформа обновлена до %s.\n", tag)
	return nil
}

// stageOffline извлекает бинари из принесённого файла. Это может быть архив
// релиза (тогда берём все бинари пакета) или один бинарь.
func stageOffline(from, stageDir string) (map[string]string, error) {
	switch strings.ToLower(filepath.Ext(from)) {
	case ".zip", ".gz", ".tgz":
		return selfupdate.StageAll(from, stageDir)
	default:
		path, err := selfupdate.StageBinary(from, stageDir)
		if err != nil {
			return nil, err
		}
		// Одиночный файл кладём под каноническим именем: Apply подменяет
		// бинари по именам, а не по тому, как файл назвали на флешке.
		dst := filepath.Join(stageDir, selfupdate.BinaryName())
		if path != dst {
			if err := copyInto(path, dst); err != nil {
				return nil, err
			}
		}
		return map[string]string{selfupdate.BinaryName(): dst}, nil
	}
}

func offlineNames(files map[string]string) []string {
	var out []string
	for _, name := range selfupdate.PackageBinaries() {
		if files[name] != "" {
			out = append(out, name)
		}
	}
	return out
}

func copyInto(src, dst string) error {
	data, err := os.ReadFile(src) //nolint:gosec // G304: путь указал администратор флагом --from
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755) //nolint:gosec // G306: это исполняемый файл
}

// resolveService вычисляет имя системной службы и URL пробы. Пустое имя —
// допустимый режим: десктопная установка под лаунчером службы не имеет, и
// подменять там нужно только файлы.
func resolveService(cmd *cobra.Command) (svcName, healthzURL string, err error) {
	svcName, _ = cmd.Flags().GetString("service")
	id, _ := cmd.Flags().GetString("id")
	port, _ := cmd.Flags().GetInt("port")

	if id != "" {
		store, sErr := launcher.NewStore()
		if sErr != nil {
			return "", "", sErr
		}
		base, gErr := store.Get(id)
		if gErr != nil {
			return "", "", fmt.Errorf("база не найдена: %w", gErr)
		}
		if svcName == "" {
			svcName = "onebase-" + slugify(base.Name)
		}
		if !cmd.Flags().Changed("port") && base.Port != 0 {
			port = base.Port
		}
	}

	healthzURL, _ = cmd.Flags().GetString("healthz-url")
	if healthzURL == "" {
		healthzURL = fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	}
	return svcName, healthzURL, nil
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
		return fmt.Errorf("обновление службы не поддерживается на %s", runtime.GOOS)
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
		return fmt.Errorf("обновление службы не поддерживается на %s", runtime.GOOS)
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
			return fmt.Errorf("служба %s не перешла в %s за %s", name, state, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
