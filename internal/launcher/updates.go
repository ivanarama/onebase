package launcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/selfupdate"
	"github.com/ivantit66/onebase/internal/version"
)

// Обновление платформы из лаунчера (план 92).
//
// Почему кнопка живёт именно здесь: лаунчер запускает базы дочерними процессами
// ТОГО ЖЕ бинаря (runner.go), поэтому он единственный, кто может корректно их
// остановить, подменить файл и поднять обратно. Процесс базы своим бинарём не
// распоряжается — он может быть системной службой, — поэтому в Предприятии
// показывается только версия.
//
// Загрузка и применение разделены намеренно: скачивание идёт фоном и ничего не
// останавливает, а в опасном окне остаётся лишь подмена файлов и перезапуск.

// updateCheckInterval — как часто лаунчер спрашивает GitHub. На канале build
// сборки выходят по нескольку раз в день, но дёргать пользователя чаще, чем
// раз в несколько часов, незачем — уведомление и так тихое.
const updateCheckInterval = 4 * time.Hour

// updateFirstCheckDelay — пауза перед первой проверкой: старт лаунчера не
// должен ждать сеть (в офлайн-контуре её может не быть вовсе).
const updateFirstCheckDelay = 5 * time.Second

// applyUpdate is a narrow test seam for launcher orchestration. The filesystem
// transaction itself remains covered by internal/selfupdate tests.
var applyUpdate = (*selfupdate.OperationLease).ApplyWithRollbackState
var recoverUpdate = (*selfupdate.OperationLease).Recover
var recoverUpdateStatus = (*selfupdate.OperationLease).RecoverWithResult
var updateBinaryDir = selfupdate.BinaryDir
var selfUpdatableDir = selfupdate.CanSafelyUpdateBinaryDir
var restartSelf = RestartSelf

var ErrBinaryRecoveryRestartRequired = errors.New("launcher restart is required after binary recovery")

// updatesVM — состояние обновлений для интерфейса.
type updatesVM struct {
	// Enabled — политика разрешает показывать средства обновления.
	Enabled bool
	// NetAllowed — политика разрешает сетевые проверки.
	NetAllowed bool
	// CanWrite — у пользователя есть право заменить бинарь. Ложь на общей
	// установке (Program Files, терминальный сервер): там платформой
	// распоряжается администратор.
	CanWrite bool
	BinDir   string

	Current       string
	Channel       string
	ChannelLocked bool
	Repo          string

	CheckedAt  time.Time
	CheckError string

	LatestTag   string
	LatestNotes string
	LatestURL   string
	LatestAt    time.Time

	// Available — есть что предложить; SameScheme отличает «более новая сборка»
	// от «другой канал предлагает другую версию».
	Available  bool
	SameScheme bool

	StagedTag string
	PrevTag   string

	// RunningCount — сколько баз будет остановлено применением обновления.
	RunningCount int
}

// updatesState собирает состояние без обращения к сети.
func (h *handler) updatesState() updatesVM {
	vm := updatesVM{Current: version.String()}

	binDir, err := updateBinaryDir()
	if err != nil {
		oblog.Component("launcher").Warn("не определён каталог бинаря", "err", err)
		return vm
	}
	vm.BinDir = binDir

	policy := selfupdate.LoadPolicy(binDir)
	vm.Enabled = policy.UIAllowed()
	vm.NetAllowed = policy.CheckAllowed()
	vm.ChannelLocked = policy.ChannelLocked()
	vm.CanWrite = selfupdate.CanSafelyUpdateBinaryDir(binDir)

	st, err := selfupdate.LoadState()
	if err != nil {
		oblog.Component("launcher").Warn("состояние обновлений не прочитано", "err", err)
	}
	vm.Channel = string(policy.ChannelOr(st.ChannelOrDefault()))
	vm.Repo = policy.RepoOr("")
	vm.CheckedAt = st.CheckedAt
	vm.CheckError = st.CheckError
	if st.Latest != nil {
		vm.LatestTag = st.Latest.Tag
		vm.LatestNotes = st.Latest.Notes
		vm.LatestURL = st.Latest.URL
		vm.LatestAt = st.Latest.PublishedAt
		vm.Available = st.UpdateAvailable(vm.Current)
		vm.SameScheme = selfupdate.SameScheme(vm.Current, st.Latest.Tag)
	}
	if st.StagedReady() {
		vm.StagedTag = st.Staged.Tag
	}
	if st.Prev != nil {
		if rollback, validateErr := selfupdate.ValidatedRollbackInfo(binDir); validateErr != nil {
			oblog.Component("launcher").Warn("rollback snapshot is unavailable; hiding it from update status", "err", validateErr)
		} else if rollback != nil {
			vm.PrevTag = rollback.Tag
		}
	}
	if h.runner != nil && h.store != nil {
		tracked := make(map[string]bool)
		for _, id := range h.runner.RunningIDs() {
			tracked[id] = true
		}
		vm.RunningCount = len(tracked)
		if bases, listErr := h.store.List(); listErr == nil {
			statuses := h.baseStatuses(bases)
			for _, base := range bases {
				if !tracked[base.ID] && statuses[base.ID].running {
					vm.RunningCount++
				}
			}
		}
	}
	return vm
}

// ShowBadge сообщает, рисовать ли отметку об обновлении в шапке лаунчера.
func (v updatesVM) ShowBadge() bool { return v.Enabled && v.Available }

// CanApply сообщает, можно ли прямо сейчас применить скачанное обновление.
func (v updatesVM) CanApply() bool {
	return v.Enabled && v.CanWrite && v.StagedTag != "" && v.StagedTag != v.Current
}

func (h *handler) updatesPage(w http.ResponseWriter, r *http.Request) {
	render(w, r, "page-updates", map[string]any{
		"Title": tr(resolveLang(r), "onebase — Обновление платформы"),
		"U":     h.updatesState(),
	})
}

func (h *handler) beginUpdateOperation(w http.ResponseWriter, r *http.Request) bool {
	if h.updateQuiescing.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Launcher is restarting after a binary update"})
		return false
	}
	if h.updateMu.TryLock() {
		return true
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": tr(resolveLang(r), "Другая операция обновления уже выполняется"),
	})
	return false
}

func (h *handler) rejectWhileUpdateQuiescing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.updateQuiescing.Load() {
			w.Header().Set("Connection", "close")
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Launcher is restarting after a binary update"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handler) updatesCheck(w http.ResponseWriter, r *http.Request) {
	if !h.beginUpdateOperation(w, r) {
		return
	}
	defer h.updateMu.Unlock()
	vm := h.updatesState()
	if !vm.Enabled || !vm.NetAllowed {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Обновление платформы запрещено политикой")})
		return
	}
	st, err := selfupdate.Check(r.Context(), selfupdate.Options{
		Repo:    vm.Repo,
		Channel: selfupdate.Channel(vm.Channel),
	})
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":        true,
		"latest":    latestTag(st),
		"available": st.UpdateAvailable(version.String()),
	})
}

func (h *handler) updatesDownload(w http.ResponseWriter, r *http.Request) {
	if !h.beginUpdateOperation(w, r) {
		return
	}
	defer h.updateMu.Unlock()
	vm := h.updatesState()
	if !vm.Enabled || !vm.NetAllowed {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Обновление платформы запрещено политикой")})
		return
	}
	// Скачивание длится минуты — контекст запроса для него не годится: клиент
	// мог закрыть вкладку, а загрузку прерывать незачем.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	rel, err := selfupdate.LatestRelease(ctx, vm.Repo, selfupdate.Channel(vm.Channel))
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	staged, err := selfupdate.Fetch(ctx, rel)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "staged": staged.Tag})
}

func (h *handler) updatesApply(w http.ResponseWriter, r *http.Request) {
	if !h.beginUpdateOperation(w, r) {
		return
	}
	defer h.updateMu.Unlock()
	vm := h.updatesState()
	if !vm.Enabled {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Обновление платформы запрещено политикой")})
		return
	}
	if !vm.CanWrite {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Нет прав на запись в каталог платформы — обратитесь к администратору")})
		return
	}
	lease, err := selfupdate.AcquireOperationLease()
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	defer func() {
		if err := lease.Release(); err != nil {
			oblog.Component("launcher").Warn("не удалось освободить блокировку обновления", "err", err)
		}
	}()
	if err := lease.ReserveTarget(vm.BinDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	st, err := selfupdate.LoadState()
	if err != nil || !st.StagedReady() {
		writeJSON(w, 409, map[string]any{"error": tr(resolveLang(r), "Обновление не скачано")})
		return
	}
	if st.RecoveryPending() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Сначала дождитесь восстановления баз после предыдущего обновления"})
		return
	}

	expectedStage := *st.Staged
	previousTag := installedVersionOr(vm.BinDir, vm.Current)
	if err := h.stopAllForUpdate(&st, func(current *selfupdate.State) error {
		if current.RecoveryPending() {
			return errors.New("не завершено восстановление баз после предыдущего обновления")
		}
		if !sameStaged(current.Staged, &expectedStage) {
			return errors.New("скачанное обновление изменилось; повторите применение")
		}
		return nil
	}, lease.ReleaseTargetReservation); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	recovered, err := lease.RecoverWithResult(vm.BinDir)
	if err != nil {
		// Consumers are already stopped. Recovery could not prove one complete
		// generation, so the lifecycle gate must remain closed.
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if recovered {
		// Recovery may have changed the generation underneath this launcher.
		// Never continue with state loaded by the old executable.
		writeJSON(w, 200, map[string]any{"ok": true, "restart": true, "recovered": true})
		h.restartAfterResponse()
		return
	}

	if err := applyUpdate(lease, expectedStage, vm.BinDir, previousTag); err != nil {
		if snapshotErr := lease.ValidateRollbackSnapshot(vm.BinDir); snapshotErr != nil {
			invalidatePrevAfterApplyFailure()
		}
		// A recovery-pending error may leave the installation between files.
		// Keep the lifecycle gate closed until a later startup recovery proves a
		// complete old or new binary set.
		if !selfupdate.RecoveryPending(err) {
			if releaseErr := lease.ReleaseTargetReservation(); releaseErr != nil {
				writeJSON(w, 500, map[string]any{"error": errors.Join(err, releaseErr).Error()})
				return
			}
			h.runner.AllowStarts()
			attempted := append([]selfupdate.RestartRecord(nil), st.RestartRecords...)
			legacy := append([]string(nil), st.RestartBases...)
			failed := h.resumeBases(attempted)
			if _, saveErr := recordRecoveryResult(attempted, failed, legacy); saveErr != nil {
				oblog.Component("launcher").Warn("не удалось обновить список восстановления", "err", saveErr)
			}
		}
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "restart": true})
	h.restartAfterResponse()
}

func (h *handler) updatesRollback(w http.ResponseWriter, r *http.Request) {
	if !h.beginUpdateOperation(w, r) {
		return
	}
	defer h.updateMu.Unlock()
	vm := h.updatesState()
	if !vm.Enabled || !vm.CanWrite {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Обновление платформы запрещено политикой")})
		return
	}
	lease, err := selfupdate.AcquireOperationLease()
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	defer func() {
		if err := lease.Release(); err != nil {
			oblog.Component("launcher").Warn("не удалось освободить блокировку обновления", "err", err)
		}
	}()
	if err := lease.ReserveTarget(vm.BinDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	st, err := selfupdate.LoadState()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if st.Prev == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Нет предыдущей версии для отката"})
		return
	}
	if st.RecoveryPending() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Сначала дождитесь восстановления баз после предыдущего обновления"})
		return
	}
	targetDir, err := selfupdate.CanonicalTargetDir(vm.BinDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if st.Prev.TargetDir == "" || st.Prev.TargetDir != targetDir {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Предыдущая версия сохранена для другой установки"})
		return
	}
	expectedPrevTag := st.Prev.Tag
	if err := h.stopAllForUpdate(&st, func(current *selfupdate.State) error {
		if current.RecoveryPending() {
			return errors.New("не завершено восстановление баз после предыдущего обновления")
		}
		if current.Prev == nil || current.Prev.Tag != expectedPrevTag || current.Prev.TargetDir != targetDir {
			return errors.New("состояние отката изменилось; повторите операцию")
		}
		return nil
	}, lease.ReleaseTargetReservation); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	recovered, err := lease.RecoverWithResult(vm.BinDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if recovered {
		writeJSON(w, 200, map[string]any{"ok": true, "restart": true, "recovered": true})
		h.restartAfterResponse()
		return
	}

	if err := lease.RollbackPrev(vm.BinDir); err != nil {
		if !selfupdate.RecoveryPending(err) {
			if releaseErr := lease.ReleaseTargetReservation(); releaseErr != nil {
				writeJSON(w, 500, map[string]any{"error": errors.Join(err, releaseErr).Error()})
				return
			}
			h.runner.AllowStarts()
			attempted := append([]selfupdate.RestartRecord(nil), st.RestartRecords...)
			legacy := append([]string(nil), st.RestartBases...)
			failed := h.resumeBases(attempted)
			if _, saveErr := recordRecoveryResult(attempted, failed, legacy); saveErr != nil {
				oblog.Component("launcher").Warn("не удалось обновить список восстановления", "err", saveErr)
			}
		}
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if _, err := selfupdate.UpdateState(func(current *selfupdate.State) error {
		if current.Prev != nil && current.Prev.Tag == expectedPrevTag {
			current.Prev = nil
		}
		return nil
	}); err != nil {
		oblog.Component("launcher").Warn("состояние обновлений не сохранено", "err", err)
	}

	writeJSON(w, 200, map[string]any{"ok": true, "restart": true})
	h.restartAfterResponse()
}

func (h *handler) updatesChannel(w http.ResponseWriter, r *http.Request) {
	if !h.beginUpdateOperation(w, r) {
		return
	}
	defer h.updateMu.Unlock()
	vm := h.updatesState()
	if !vm.Enabled {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Обновление платформы запрещено политикой")})
		return
	}
	if vm.ChannelLocked {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Канал обновлений задан администратором")})
		return
	}
	ch := selfupdate.Channel(r.URL.Query().Get("value"))
	if ch != selfupdate.ChannelBuild && ch != selfupdate.ChannelStable {
		writeJSON(w, 400, map[string]any{"error": "unknown channel"})
		return
	}
	_, err := selfupdate.UpdateState(func(st *selfupdate.State) error {
		st.Channel = ch
		// Скачанное принадлежало прежнему каналу — предлагать его больше нельзя.
		st.Staged = nil
		st.Latest = nil
		return nil
	})
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "channel": string(ch)})
}

// stopAllForUpdate под lifecycle lease делает полный preflight, сохраняет
// точный recovery-list и лишь затем останавливает tracked + adopted базы. При
// частичном runtime-сбое уже остановленные базы поднимаются обратно.
func (h *handler) stopAllForUpdate(st *selfupdate.State, guard func(*selfupdate.State) error, beforeResume ...func() error) error {
	if err := h.runner.holdStarts(); err != nil {
		return err
	}
	leaseOwned := true
	defer func() {
		if leaseOwned {
			h.runner.AllowStarts()
		}
	}()

	bases, _, err := h.store.Snapshot()
	if err != nil {
		return err
	}
	tracked := make([]bool, len(bases))
	for i, base := range bases {
		if base != nil && h.runner.tracksBaseGeneration(base) {
			tracked[i] = true
		}
	}
	statuses := make([]BaseRuntimeStatus, len(bases))
	var wg sync.WaitGroup
	for i, base := range bases {
		if base == nil || tracked[i] {
			continue
		}
		i, base := i, base
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = h.runner.RuntimeStatus(base)
		}()
	}
	wg.Wait()
	for i, base := range bases {
		if base == nil || tracked[i] {
			continue
		}
		status := statuses[i]
		if status.Occupied && !status.Controllable {
			return fmt.Errorf("база %q или её порт %d заняты процессом без подтверждённого безопасного управления",
				base.Name, base.Port)
		}
	}
	restartRecords := make([]selfupdate.RestartRecord, 0, len(bases))
	for i, base := range bases {
		if base == nil || (!tracked[i] && !statuses[i].Controllable) {
			continue
		}
		record, err := restartRecordForBase(base)
		if err != nil {
			return err
		}
		restartRecords = append(restartRecords, record)
	}
	restartRecords = mergeRestartRecords(restartRecords)
	// Recovery state is a prerequisite, not best effort: after binary replace
	// only the new launcher can resume the stopped bases.
	updated, err := selfupdate.UpdateState(func(current *selfupdate.State) error {
		if guard != nil {
			if err := guard(current); err != nil {
				return err
			}
		}
		// ID-only records cannot prove which Store generation was stopped. New
		// operations publish only generation-bound records.
		current.RestartBases = nil
		current.RestartRecords = mergeRestartRecords(current.RestartRecords, restartRecords)
		return nil
	})
	if err != nil {
		return fmt.Errorf("сохранить список баз для восстановления: %w", err)
	}
	*st = updated

	// stopAllHeld releases the lease on error and retains it on success.
	leaseOwned = false
	if err := h.runner.stopAllHeld(bases, true); err != nil {
		if len(beforeResume) > 0 && beforeResume[0] != nil {
			if releaseErr := beforeResume[0](); releaseErr != nil {
				return errors.Join(err, fmt.Errorf("release update target before resuming bases: %w", releaseErr))
			}
		}
		attempted := append([]selfupdate.RestartRecord(nil), st.RestartRecords...)
		legacy := append([]string(nil), st.RestartBases...)
		failed := h.resumeBases(attempted)
		updated, saveErr := recordRecoveryResult(attempted, failed, legacy)
		if saveErr != nil {
			return errors.Join(err, fmt.Errorf("сохранить результат восстановления баз: %w", saveErr))
		}
		*st = updated
		return err
	}
	return nil
}

func sameStaged(a, b *selfupdate.StagedInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Tag == b.Tag && a.Dir == b.Dir && a.Verified == b.Verified &&
		a.StagedAt.Equal(b.StagedAt) && slices.Equal(a.Files, b.Files)
}

func installedVersionOr(targetDir, fallback string) string {
	got, err := selfupdate.BinaryVersion(filepath.Join(targetDir, selfupdate.BinaryName()))
	if err != nil || got == "" {
		return fallback
	}
	return got
}

// invalidatePrevAfterApplyFailure fails closed: Apply can consume the single
// process-wide rollback snapshot before a later swap step fails. Its error
// alone cannot prove that the old snapshot is still usable.
func invalidatePrevAfterApplyFailure() {
	if _, err := selfupdate.UpdateState(func(current *selfupdate.State) error {
		current.Prev = nil
		return nil
	}); err != nil {
		oblog.Component("launcher").Warn("failed to clear untrustworthy rollback state", "err", err)
	}
}

const (
	restartGenerationDomain = "onebase/restart-record/v1\x00"
	restartGenerationPrefix = "ct1:"
)

func restartGeneration(controlToken string) string {
	if controlToken == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(restartGenerationDomain + controlToken))
	return restartGenerationPrefix + hex.EncodeToString(sum[:])
}

func validRestartRecord(record selfupdate.RestartRecord) bool {
	if record.ID == "" || len(record.Generation) != len(restartGenerationPrefix)+sha256.Size*2 ||
		record.Generation[:len(restartGenerationPrefix)] != restartGenerationPrefix {
		return false
	}
	_, err := hex.DecodeString(record.Generation[len(restartGenerationPrefix):])
	return err == nil
}

func restartRecordForBase(base *Base) (selfupdate.RestartRecord, error) {
	if base == nil || base.ID == "" || base.ControlToken == "" {
		return selfupdate.RestartRecord{}, errors.New("нельзя сохранить recovery-запись базы без ID и control token")
	}
	return selfupdate.RestartRecord{ID: base.ID, Generation: restartGeneration(base.ControlToken)}, nil
}

func mergeRestartRecords(groups ...[]selfupdate.RestartRecord) []selfupdate.RestartRecord {
	set := make(map[selfupdate.RestartRecord]struct{})
	for _, group := range groups {
		for _, record := range group {
			if validRestartRecord(record) {
				set[record] = struct{}{}
			}
		}
	}
	records := make([]selfupdate.RestartRecord, 0, len(set))
	for record := range set {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].ID == records[j].ID {
			return records[i].Generation < records[j].Generation
		}
		return records[i].ID < records[j].ID
	})
	return records
}

// recordRecoveryResult removes only successfully resumed exact generations.
// Records published by a concurrent operation remain intact. Legacy ID-only
// records from the loaded snapshot are consumed fail-closed and never started.
func recordRecoveryResult(attempted, failed []selfupdate.RestartRecord, legacyAttempted []string) (selfupdate.State, error) {
	attemptedSet := make(map[selfupdate.RestartRecord]struct{}, len(attempted))
	for _, record := range attempted {
		attemptedSet[record] = struct{}{}
	}
	failedSet := make(map[selfupdate.RestartRecord]struct{}, len(failed))
	for _, record := range failed {
		failedSet[record] = struct{}{}
	}
	legacySet := make(map[string]struct{}, len(legacyAttempted))
	for _, id := range legacyAttempted {
		legacySet[id] = struct{}{}
	}
	return selfupdate.UpdateState(func(current *selfupdate.State) error {
		remaining := make([]selfupdate.RestartRecord, 0, len(current.RestartRecords)+len(failed))
		for _, record := range current.RestartRecords {
			_, wasAttempted := attemptedSet[record]
			_, didFail := failedSet[record]
			if !wasAttempted || didFail {
				remaining = append(remaining, record)
			}
		}
		current.RestartRecords = mergeRestartRecords(remaining)

		legacyRemaining := make([]string, 0, len(current.RestartBases))
		for _, id := range current.RestartBases {
			if _, consumed := legacySet[id]; !consumed {
				legacyRemaining = append(legacyRemaining, id)
			}
		}
		current.RestartBases = mergeBaseIDs(legacyRemaining)
		return nil
	})
}

func mergeBaseIDs(groups ...[]string) []string {
	set := make(map[string]struct{})
	for _, group := range groups {
		for _, id := range group {
			if id != "" {
				set[id] = struct{}{}
			}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// resumeBases returns exact records whose start should be retried. A missing,
// malformed, or generation-mismatched record is consumed fail-closed.
// старте launcher. Исчезнувшие из Store базы намеренно не зацикливаются.
func (h *handler) resumeBases(records []selfupdate.RestartRecord) []selfupdate.RestartRecord {
	retryable := mergeRestartRecords(records)
	if len(retryable) == 0 {
		return nil
	}
	// Recovery is a lifecycle operation just like edit/delete/restart. Holding
	// the gate makes the Store re-read below authoritative with respect to those
	// operations, and startHeld avoids trying to acquire the same lease twice.
	if h.store == nil || h.runner == nil {
		return retryable
	}
	if err := h.runner.holdStarts(); err != nil {
		oblog.Component("launcher").Warn("base recovery is waiting for another lifecycle operation", "err", err)
		return retryable
	}
	defer h.runner.AllowStarts()

	var failed []selfupdate.RestartRecord
	for _, record := range retryable {
		var (
			base           *Base
			alreadyRunning bool
			startErr       error
		)
		err := h.store.withBaseReadLock(record.ID, func(current *Base) error {
			base = current
			// Keep the Store lock from this comparison through Start. Otherwise a
			// concurrent Remove+Add could reuse the ID after the comparison and
			// recovery would launch a snapshot that is no longer registered.
			if restartGeneration(current.ControlToken) != record.Generation {
				return nil
			}
			if status := h.runner.RuntimeStatus(current); status.Running && status.Controllable {
				alreadyRunning = true
				return nil
			}
			startErr = h.runner.startHeld(current)
			return nil
		})
		if err != nil {
			if !errors.Is(err, ErrBaseNotFound) {
				oblog.Component("launcher").Warn("не удалось прочитать базу для восстановления", "baseID", record.ID, "err", err)
				failed = append(failed, record)
			}
			continue
		}
		// Compare before any Store mutation. An empty/different token can be a
		// replacement record reusing the old ID and must never be initialized or
		// started by recovery.
		if restartGeneration(base.ControlToken) != record.Generation {
			oblog.Component("launcher").Warn("запись recovery не совпала с текущим поколением базы; запуск пропущен", "baseID", record.ID)
			continue
		}
		if alreadyRunning {
			continue
		}
		if err := startErr; err != nil {
			oblog.Component("launcher").Warn("база не поднялась после обновления", "base", base.Name, "err", err)
			failed = append(failed, record)
			continue
		}
		if err := h.runner.WaitReady(base, 15*time.Second); err != nil {
			oblog.Component("launcher").Warn("база не стала готова после обновления", "base", base.Name, "err", err)
			failed = append(failed, record)
		}
	}
	return failed
}

// restartAfterResponse запускает новый процесс из подменённого бинаря и просит
// текущий закрыться. Пауза даёт HTTP-ответу дойти до страницы, которая покажет
// «перезапуск» до того, как окно закроется.
func (h *handler) restartAfterResponse() {
	h.updateQuiescing.Store(true)
	go func() {
		time.Sleep(700 * time.Millisecond)
		if err := restartSelf(); err != nil {
			oblog.Component("launcher").Error("не удалось перезапустить лаунчер после обновления", "err", err)
			// The on-disk generation has changed. Keep starts blocked and recovery
			// records intact until a process launched from that generation resumes.
			if h.quitFn != nil {
				h.quitFn()
			}
			return
		}
		if h.quitFn != nil {
			h.quitFn()
		}
	}()
}

// RestartSelf запускает новый экземпляр onebase с теми же аргументами. После
// подмены файла по этому пути лежит уже новая версия, поэтому «перезапустить
// себя» и «запустить обновлённую платформу» — одно и то же действие.
func RestartSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...) //nolint:gosec // G204: путь — собственный исполняемый файл, аргументы — свои же
	cmd.Env = append(os.Environ(), RestartWaitEnv+"=1")
	noWindow(cmd)
	return cmd.Start()
}

// ResumeAfterUpdate поднимает базы, работавшие до перезапуска ради обновления.
// Вызывается при старте лаунчера, до открытия окна.
func ResumeAfterUpdate(store *Store, runner *Runner) (resultErr error) {
	lease, leaseErr := selfupdate.AcquireOperationLease()
	if leaseErr != nil {
		oblog.Component("launcher").Warn("восстановление баз ждёт другой операции обновления", "err", leaseErr)
		return leaseErr
	}
	defer func() {
		if err := lease.Release(); err != nil {
			oblog.Component("launcher").Warn("не удалось освободить блокировку восстановления", "err", err)
		}
	}()
	binDir, err := updateBinaryDir()
	if err != nil {
		oblog.Component("launcher").Warn("cannot resolve the installation directory for update recovery", "err", err)
		return err
	}
	// Установка вне приватного пользовательского каталога (C:\onebase, Program
	// Files, сетевая шара) в протоколе самообновления не участвует вовсе: там
	// нельзя ни взять блокировки координации, ни заменить бинарь. Значит и
	// прерванному обновлению взяться неоткуда — восстанавливать нечего.
	//
	// Раньше эта невозможность приезжала из ReserveTarget обычной ошибкой и
	// валила старт лаунчера целиком: сборки 783–792 не запускались ни из
	// C:\onebase (куда распаковать велит README), ни с любого другого пути вне
	// %USERPROFILE%. Проверка стоит до recoverUpdateStatus, потому что
	// резервирование каталога там выполняется раньше, чем проверка «есть ли
	// вообще что восстанавливать».
	if !selfUpdatableDir(binDir) {
		oblog.Component("launcher").Info(
			"самообновление для этой установки недоступно — восстановление обновления пропущено",
			"dir", binDir)
		return resumePendingBases(store, runner)
	}
	recovered, err := recoverUpdateStatus(lease, binDir)
	if err != nil {
		oblog.Component("launcher").Error("cannot recover an interrupted binary update", "err", err)
		return fmt.Errorf("recover interrupted binary update: %w", err)
	}
	if recovered {
		return ErrBinaryRecoveryRestartRequired
	}
	if err := lease.ReleaseTargetReservation(); err != nil {
		return fmt.Errorf("release binary recovery target before resuming bases: %w", err)
	}
	return resumePendingBases(store, runner)
}

// resumePendingBases поднимает базы, помеченные к перезапуску в состоянии
// обновления. Общий хвост ResumeAfterUpdate для обеих веток: и когда
// восстановление бинаря отработало, и когда его не могло быть в принципе.
func resumePendingBases(store *Store, runner *Runner) error {
	st, err := selfupdate.LoadState()
	if err != nil || !st.RecoveryPending() {
		return err
	}
	records := append([]selfupdate.RestartRecord(nil), st.RestartRecords...)
	legacy := append([]string(nil), st.RestartBases...)
	h := &handler{store: store, runner: runner}
	failed := h.resumeBases(records)
	_, err = recordRecoveryResult(records, failed, legacy)
	return err
}

// ApplyStagedOnStart применяет скачанное обновление до открытия окна, только
// если ни одна зарегистрированная база не пережила предыдущий launcher в
// фоне. Работает лишь при включённом auto_apply: на build-канале молча менять
// платформу нельзя.
// Возвращает true, если бинарь заменён и процесс нужно перезапустить.
func ApplyStagedOnStart(store *Store, runner *Runner) bool {
	if store == nil || runner == nil {
		return false
	}
	lease, err := selfupdate.AcquireOperationLease()
	if err != nil {
		oblog.Component("launcher").Warn("автообновление ждёт другую операцию", "err", err)
		return false
	}
	defer func() {
		if err := lease.Release(); err != nil {
			oblog.Component("launcher").Warn("не удалось освободить блокировку автообновления", "err", err)
		}
	}()
	binDir, err := updateBinaryDir()
	if err != nil {
		return false
	}
	// Заменить бинарь в такой установке всё равно нельзя — применять нечего.
	// Без этой проверки старт каждый раз писал в журнал ERROR о «неудавшемся
	// восстановлении», хотя восстанавливать было нечего (см. ResumeAfterUpdate).
	if !selfUpdatableDir(binDir) {
		return false
	}
	if err := recoverUpdate(lease, binDir); err != nil {
		oblog.Component("launcher").Error("cannot recover an interrupted binary update", "err", err)
		return false
	}
	st, err := selfupdate.LoadState()
	if err != nil || !st.AutoApply || !st.StagedReady() || st.RecoveryPending() {
		return false
	}
	expectedStage := *st.Staged
	if st.Staged.Tag == version.String() {
		// Уже работаем на этой версии — просто прибираем.
		if _, err := selfupdate.UpdateState(func(current *selfupdate.State) error {
			if sameStaged(current.Staged, &expectedStage) {
				current.Staged = nil
			}
			return nil
		}); err != nil {
			oblog.Component("launcher").Warn("состояние обновлений не сохранено", "err", err)
		}
		return false
	}
	if !selfupdate.StagedFilesAvailable(expectedStage) {
		return false
	}
	if !selfupdate.LoadPolicy(binDir).UIAllowed() || !selfupdate.CanSafelyUpdateBinaryDir(binDir) {
		return false
	}
	if err := runner.holdStarts(); err != nil {
		return false
	}
	releaseStarts := true
	defer func() {
		if releaseStarts {
			runner.AllowStarts()
		}
	}()
	bases, _, err := store.Snapshot()
	if err != nil {
		return false
	}
	for _, base := range bases {
		if base != nil && runner.RuntimeStatus(base).Occupied {
			// on_close=background intentionally lets bases outlive the launcher;
			// never replace their executable package underneath them.
			return false
		}
	}
	previousTag := installedVersionOr(binDir, version.String())
	if err := applyUpdate(lease, expectedStage, binDir, previousTag); err != nil {
		if snapshotErr := lease.ValidateRollbackSnapshot(binDir); snapshotErr != nil {
			invalidatePrevAfterApplyFailure()
		}
		oblog.Component("launcher").Warn("обновление не применено при старте", "err", err)
		if selfupdate.RecoveryPending(err) {
			releaseStarts = false
		}
		return false
	}
	releaseStarts = false
	return true
}

// startUpdateWatcher включает тихую фоновую проверку обновлений.
func (h *handler) startUpdateWatcher() {
	go func() {
		time.Sleep(updateFirstCheckDelay)
		for {
			h.checkUpdatesQuiet()
			time.Sleep(updateCheckInterval)
		}
	}()
}

// checkUpdatesQuiet спрашивает GitHub и молча складывает ответ в состояние.
// Ошибка сети здесь — нормальный исход (офлайн-машина, прокси), в интерфейс она
// попадёт строкой «проверить не удалось», а не всплывающей ошибкой.
func (h *handler) checkUpdatesQuiet() {
	if !h.updateMu.TryLock() {
		return
	}
	defer h.updateMu.Unlock()
	binDir, err := selfupdate.BinaryDir()
	if err != nil {
		return
	}
	policy := selfupdate.LoadPolicy(binDir)
	if !policy.UIAllowed() || !policy.CheckAllowed() {
		return
	}
	// Сборка без -ldflags — локальная, разработчика: обновлять её всё равно
	// нельзя (Newer отказывает на dev-*), а лишний исходящий запрос из
	// dev-окружения не нужен.
	if version.Build == "" {
		return
	}
	st, err := selfupdate.LoadState()
	if err != nil {
		oblog.Component("launcher").Warn("состояние обновлений не прочитано", "err", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := selfupdate.Check(ctx, selfupdate.Options{
		Repo:    policy.RepoOr(""),
		Channel: policy.ChannelOr(st.ChannelOrDefault()),
	}); err != nil {
		oblog.Component("launcher").Debug("проверка обновлений не удалась", "err", err)
	}
}

func latestTag(st selfupdate.State) string {
	if st.Latest == nil {
		return ""
	}
	return st.Latest.Tag
}
