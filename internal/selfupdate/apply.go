package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/fsmode"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/version"
)

// Options — что и откуда обновляем. Пустые поля берутся из состояния и
// политики, поэтому вызывающему обычно достаточно Options{}.
type Options struct {
	Repo    string
	Channel Channel
}

// Check спрашивает у GitHub последнюю версию канала и сохраняет результат в
// состоянии. Ошибка сети тоже сохраняется — интерфейсу важно отличать «нет
// обновлений» от «не смогли проверить».
func Check(ctx context.Context, opts Options) (State, error) {
	return check(ctx, opts, LatestRelease)
}

func check(ctx context.Context, opts Options, latest func(context.Context, string, Channel) (Release, error)) (result State, resultErr error) {
	current := version.String()
	var (
		ch         Channel
		generation uint64
		oldStage   string
	)
	started, err := updateStateRecovering(func(st *State) error {
		ch = opts.Channel
		if ch == "" {
			ch = st.ChannelOrDefault()
		}
		if st.ChannelOrDefault() != ch {
			if st.Staged != nil {
				oldStage = st.Staged.Dir
			}
			st.Latest = nil
			st.Staged = nil
		}
		st.Channel = ch
		st.CheckGeneration++
		if st.CheckGeneration == 0 {
			st.CheckGeneration = 1
		}
		generation = st.CheckGeneration
		return nil
	})
	if err != nil {
		return started, err
	}
	if oldStage != "" {
		stageLock, lockErr := acquireStageOperationLock()
		if lockErr != nil {
			return started, lockErr
		}
		removeManagedStage(oldStage)
		if unlockErr := stageLock.Unlock(); unlockErr != nil {
			return started, unlockErr
		}
	}
	checkedAt := time.Now().UTC()

	rel, err := latest(ctx, opts.Repo, ch)
	if err != nil {
		checkErr := err
		// Сохранить состояние всё равно нужно: иначе UI не узнает, что
		// проверка была и провалилась.
		updated, saveErr := updateStateRecovering(func(latestState *State) error {
			if latestState.CheckGeneration != generation || latestState.Channel != ch {
				return nil
			}
			latestState.Current = current
			latestState.CheckedAt = checkedAt
			latestState.CheckError = checkErr.Error()
			return nil
		})
		if saveErr != nil {
			oblog.Component("selfupdate").Warn("состояние обновлений не сохранено", "err", saveErr)
			return started, checkErr
		}
		return updated, checkErr
	}
	stageLock, stageLockErr := acquireStageOperationLock()
	if stageLockErr != nil {
		return started, stageLockErr
	}
	defer func() { resultErr = errors.Join(resultErr, stageLock.Unlock()) }()

	latestInfo := &RelInfo{
		Tag:         rel.Tag,
		PublishedAt: rel.PublishedAt,
		Notes:       rel.Notes,
		URL:         rel.HTMLURL,
	}
	var obsoleteStage string
	updated, saveErr := updateStateRecovering(func(latestState *State) error {
		if latestState.CheckGeneration != generation || latestState.Channel != ch {
			return nil
		}
		latestState.Current = current
		latestState.CheckedAt = checkedAt
		latestState.CheckError = ""
		latestState.Latest = latestInfo
		// Скачанное ранее обновление другой версии больше не актуально: канал мог
		// переключиться, а сборка — уехать вперёд.
		if latestState.Staged != nil && latestState.Staged.Tag != rel.Tag {
			obsoleteStage = latestState.Staged.Dir
			latestState.Staged = nil
		}
		return nil
	})
	if saveErr == nil && obsoleteStage != "" {
		removeManagedStage(obsoleteStage)
	}
	return updated, saveErr
}

// Fetch скачивает релиз, сверяет контрольную сумму, распаковывает бинари в
// staging и убеждается, что распакованный бинарь действительно той версии, за
// которую себя выдаёт. Только после этого обновление помечается готовым.
func Fetch(ctx context.Context, rel Release) (result StagedInfo, resultErr error) {
	stageLock, err := acquireStageOperationLock()
	if err != nil {
		return StagedInfo{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, stageLock.Unlock()) }()

	stateAtStart, err := LoadState()
	if err != nil {
		var invalid *invalidStateError
		if !errors.As(err, &invalid) {
			return StagedInfo{}, err
		}
		oblog.Component("selfupdate").Warn("состояние обновлений перезаписывается", "err", err)
		stateAtStart = State{}
	}
	if relChannel, known := releaseTagChannel(rel.Tag); known && relChannel != stateAtStart.ChannelOrDefault() {
		return StagedInfo{}, fmt.Errorf("selfupdate: релиз %s не относится к выбранному каналу %s", rel.Tag, stateAtStart.ChannelOrDefault())
	}
	if stateAtStart.Latest != nil && stateAtStart.Latest.Tag != rel.Tag && !Newer(stateAtStart.Latest.Tag, rel.Tag) {
		return StagedInfo{}, fmt.Errorf("selfupdate: релиз %s устарел, последний известный релиз — %s", rel.Tag, stateAtStart.Latest.Tag)
	}
	if stateAtStart.Staged != nil && stateAtStart.Staged.Tag == rel.Tag && stagedFilesAvailable(*stateAtStart.Staged) {
		return *stateAtStart.Staged, nil
	}
	startChannel := stateAtStart.Channel
	startGeneration := stateAtStart.CheckGeneration
	startLatestTag := stateLatestTag(stateAtStart)

	stageDir, err := newStageDir()
	if err != nil {
		return StagedInfo{}, err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			removeStage(stageDir)
		}
	}()

	archive, err := Download(ctx, rel, stageDir)
	if err != nil {
		return StagedInfo{}, err
	}
	files, err := StageAll(archive, stageDir)
	if err != nil {
		return StagedInfo{}, err
	}
	// Архив больше не нужен — он весит десятки мегабайт.
	_ = os.Remove(archive)

	got, err := binaryVersion(files[BinaryName()])
	if err != nil {
		return StagedInfo{}, err
	}
	if got != rel.Tag {
		return StagedInfo{}, fmt.Errorf("selfupdate: скачанный бинарь сообщает версию %q, ожидалась %q", got, rel.Tag)
	}

	staged := StagedInfo{
		Tag:      rel.Tag,
		Dir:      stageDir,
		Files:    sortedNames(files),
		Verified: true,
		StagedAt: time.Now().UTC(),
	}

	var obsoleteStage string
	if _, err := updateStateRecovering(func(st *State) error {
		if st.Channel != startChannel || st.CheckGeneration != startGeneration || stateLatestTag(*st) != startLatestTag {
			return fmt.Errorf("selfupdate: состояние канала изменилось во время скачивания %s", rel.Tag)
		}
		if st.Staged != nil && st.Staged.Dir != "" && st.Staged.Dir != staged.Dir {
			obsoleteStage = st.Staged.Dir
		}
		st.Latest = &RelInfo{Tag: rel.Tag, PublishedAt: rel.PublishedAt, Notes: rel.Notes, URL: rel.HTMLURL}
		st.Staged = &staged
		return nil
	}); err != nil {
		return staged, err
	}
	keepStage = true
	if obsoleteStage != "" {
		removeManagedStage(obsoleteStage)
	}
	return staged, nil
}

func stateLatestTag(st State) string {
	if st.Latest == nil {
		return ""
	}
	return st.Latest.Tag
}

func releaseTagChannel(tag string) (Channel, bool) {
	switch {
	case strings.HasPrefix(tag, "build-"):
		return ChannelBuild, true
	case strings.HasPrefix(tag, "v"):
		return ChannelStable, true
	default:
		return "", false
	}
}

func stagedFilesAvailable(staged StagedInfo) bool {
	if !staged.Verified || staged.Dir == "" || len(staged.Files) == 0 {
		return false
	}
	foundBinary := false
	for _, name := range staged.Files {
		if name == "" || filepath.Base(name) != name {
			return false
		}
		info, err := os.Stat(filepath.Join(staged.Dir, name)) //nolint:gosec // G304: name is constrained to a base name in our staging directory
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		if name == BinaryName() {
			foundBinary = true
		}
	}
	return foundBinary
}

// StagedFilesAvailable verifies that every recorded staging file still exists
// and that the package contains the main executable.
func StagedFilesAvailable(staged StagedInfo) bool {
	return stagedFilesAvailable(staged)
}

// Apply подменяет бинари платформы в targetDir содержимым staging, складывая
// прежние в ~/.onebase/updates/prev для отката.
//
// Процессы к этому моменту должны быть остановлены вызывающим: CLI гасит
// системную службу, лаунчер — дочерние процессы баз. Здесь только файлы —
// ровно та часть, которую можно покрыть тестами на всех платформах.
//
// Если подмена сорвалась на середине (второй бинарь не записался), уже
// заменённые возвращаются на место: платформа из двух разных версий хуже, чем
// неудавшееся обновление.
func Apply(staged StagedInfo, targetDir string) (resultErr error) {
	lease, err := AcquireOperationLease()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lease.Release()) }()
	return lease.Apply(staged, targetDir)
}

// Apply replaces binaries while an OperationLease is already held.
func (l *OperationLease) Apply(staged StagedInfo, targetDir string) error {
	if !l.valid() {
		return errors.New("selfupdate: operation lease is not held")
	}
	if err := l.bindTarget(targetDir); err != nil {
		return err
	}
	return applyLocked(staged, l.targetDir)
}

// ApplyWithRollbackState applies a complete package and durably publishes the
// matching rollback metadata in State. Callers that know the installed release
// tag should use this method so a crash between the file swap and their next
// state write cannot lose or mislabel the rollback snapshot.
func (l *OperationLease) ApplyWithRollbackState(staged StagedInfo, targetDir, previousTag string) error {
	if !l.valid() {
		return errors.New("selfupdate: operation lease is not held")
	}
	if strings.TrimSpace(previousTag) == "" {
		return errors.New("selfupdate: previous release tag is empty")
	}
	if !staged.Verified {
		return fmt.Errorf("selfupdate: update %s is not verified", staged.Tag)
	}
	if strings.TrimSpace(staged.Tag) == "" {
		return errors.New("selfupdate: staged release tag is empty")
	}
	if err := l.bindTarget(targetDir); err != nil {
		return err
	}
	targetDir = l.targetDir
	recovered, err := recoverUpdateTransactionLockedWithResult(targetDir)
	if err != nil {
		return err
	}
	if recovered {
		return ErrRecoveredGenerationChanged
	}
	names, err := validateApplyInputs(staged, targetDir)
	if err != nil {
		return err
	}
	if err := runApplyTransaction(staged, targetDir, names, previousTag); err != nil {
		return err
	}
	removeManagedStage(staged.Dir)
	return nil
}

func applyLocked(staged StagedInfo, targetDir string) error {
	if !staged.Verified {
		return fmt.Errorf("selfupdate: update %s is not verified", staged.Tag)
	}
	if strings.TrimSpace(staged.Tag) == "" {
		return errors.New("selfupdate: staged release tag is empty")
	}
	recovered, err := recoverUpdateTransactionLockedWithResult(targetDir)
	if err != nil {
		return err
	}
	if recovered {
		return ErrRecoveredGenerationChanged
	}
	names, err := validateApplyInputs(staged, targetDir)
	if err != nil {
		return err
	}
	if err := runApplyTransaction(staged, targetDir, names, ""); err != nil {
		return err
	}
	removeManagedStage(staged.Dir)
	return nil
}

const prevTargetFileName = ".target.json"

type prevTargetManifest struct {
	TargetDir string `json:"target_dir"`
}

// CanonicalTargetDir returns a stable identity for an installation directory.
// Windows paths are case-insensitive, so their identity is normalized too.
func CanonicalTargetDir(targetDir string) (string, error) {
	abs, err := filepath.Abs(targetDir)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = filepath.Clean(resolved)
	} else if !os.IsNotExist(resolveErr) {
		return "", resolveErr
	}
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs, nil
}

func writePrevTarget(prev, targetDir string) error {
	canonical, err := CanonicalTargetDir(targetDir)
	if err != nil {
		return err
	}
	data, err := json.Marshal(prevTargetManifest{TargetDir: canonical})
	if err != nil {
		return err
	}
	return writeFile(bytes.NewReader(data), filepath.Join(prev, prevTargetFileName), fsmode.SecretFile)
}

func validatePrevTarget(prev, targetDir string) error {
	var manifest prevTargetManifest
	if err := readStrictJSONFile(filepath.Join(prev, prevTargetFileName), &manifest); err != nil {
		return fmt.Errorf("selfupdate: назначение сохранённого отката повреждено: %w", err)
	}
	want, err := CanonicalTargetDir(targetDir)
	if err != nil {
		return err
	}
	if manifest.TargetDir == "" || manifest.TargetDir != want {
		return fmt.Errorf("selfupdate: откат сохранён для %q, а не для %q", manifest.TargetDir, want)
	}
	return nil
}

// validateApplyInputs completes every non-mutating check before Apply removes
// the previous rollback snapshot. This makes a repeated Apply of a stage that
// another process has already consumed fail without destroying that snapshot.
func validateApplyInputs(staged StagedInfo, targetDir string) ([]string, error) {
	if err := validatePlainDirectory(staged.Dir); err != nil {
		return nil, fmt.Errorf("selfupdate: staging directory is unsafe: %w", err)
	}
	if pathsOverlap(staged.Dir, targetDir) {
		return nil, fmt.Errorf("selfupdate: staging %s пересекается с каталогом установки %s", staged.Dir, targetDir)
	}
	if prev, err := PrevDir(); err != nil {
		return nil, err
	} else if pathsOverlap(staged.Dir, prev) {
		return nil, fmt.Errorf("selfupdate: staging %s пересекается с каталогом отката %s", staged.Dir, prev)
	}
	seen := make(map[string]struct{}, len(staged.Files))
	allowed := make(map[string]struct{}, len(PackageBinaries()))
	for _, name := range PackageBinaries() {
		allowed[canonicalTransactionName(name)] = struct{}{}
	}
	names := make([]string, 0, len(staged.Files))
	for _, name := range staged.Files {
		if err := validateTransactionName(name); err != nil {
			return nil, fmt.Errorf("selfupdate: недопустимое имя бинаря %q", name)
		}
		key := canonicalTransactionName(name)
		if _, ok := allowed[key]; !ok {
			return nil, fmt.Errorf("selfupdate: binary %q is not part of this platform package", name)
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("selfupdate: бинарь %q указан в staging дважды", name)
		}
		seen[key] = struct{}{}

		srcInfo, err := os.Lstat(filepath.Join(staged.Dir, name))
		if err != nil {
			return nil, fmt.Errorf("selfupdate: бинарь staging %q недоступен: %w", name, err)
		}
		if !srcInfo.Mode().IsRegular() {
			return nil, fmt.Errorf("selfupdate: бинарь staging %q не является обычным файлом", name)
		}
		targetInfo, err := os.Lstat(filepath.Join(targetDir, name))
		if os.IsNotExist(err) {
			// Такого бинаря в установке нет (например, поставили без GUI) —
			// не добавляем его, чтобы обновление не меняло состав установки.
			continue
		} else if err != nil {
			return nil, fmt.Errorf("selfupdate: проверить установленный бинарь %q: %w", name, err)
		}
		if !targetInfo.Mode().IsRegular() {
			return nil, fmt.Errorf("selfupdate: установленный бинарь %q не является обычным файлом", name)
		}
		names = append(names, name)
	}
	// Never leave an installed companion executable at the old version. Older
	// packages may omit the GUI binary, but such a package is only safe for an
	// installation that does not contain that companion.
	for _, required := range PackageBinaries() {
		if _, included := seen[canonicalTransactionName(required)]; included {
			continue
		}
		info, statErr := os.Lstat(filepath.Join(targetDir, required))
		switch {
		case os.IsNotExist(statErr):
			continue
		case statErr != nil:
			return nil, fmt.Errorf("selfupdate: проверить установленный бинарь %q: %w", required, statErr)
		case !info.Mode().IsRegular():
			return nil, fmt.Errorf("selfupdate: установленный бинарь %q не является обычным файлом", required)
		default:
			return nil, fmt.Errorf("selfupdate: пакет не содержит установленный бинарь %q", required)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("selfupdate: в %s не найдено ни одного бинаря платформы", targetDir)
	}
	return names, nil
}

func pathsOverlap(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil || absA == "" || absB == "" {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(absA); err == nil {
		absA = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absB); err == nil {
		absB = resolved
	}
	contains := func(parent, child string) bool {
		rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
		return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
	}
	return contains(absA, absB) || contains(absB, absA)
}

// RollbackPrev возвращает бинари предыдущей версии из ~/.onebase/updates/prev.
func RollbackPrev(targetDir string) (resultErr error) {
	lease, err := AcquireOperationLease()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lease.Release()) }()
	return lease.RollbackPrev(targetDir)
}

// RollbackPrev restores the previous binaries while an OperationLease is held.
func (l *OperationLease) RollbackPrev(targetDir string) error {
	if !l.valid() {
		return errors.New("selfupdate: operation lease is not held")
	}
	if err := l.bindTarget(targetDir); err != nil {
		return err
	}
	return rollbackPrevLocked(l.targetDir)
}

func rollbackPrevLocked(targetDir string) error {
	recovered, err := recoverUpdateTransactionLockedWithResult(targetDir)
	if err != nil {
		return err
	}
	if recovered {
		return ErrRecoveredGenerationChanged
	}
	return runRollbackTransaction(targetDir)
}

// binaryVersion — точка подмены в тестах: проверку «скачали не ту версию»
// нужно покрыть, не собирая настоящий бинарь под каждую платформу.
var binaryVersion = BinaryVersion

// BinaryVersion спрашивает у бинаря его версию (`onebase --version`). Служит
// двум целям: проверить, что скачали то, что заявлено, и убедиться, что новый
// файл вообще запускается на этой машине.
func BinaryVersion(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("selfupdate: путь к бинарю пуст")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version") //nolint:gosec // G204: путь к бинарю собран нами (staging или каталог установки), не из пользовательского ввода
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("не удалось определить версию бинаря %s: %w: %s", filepath.Base(path), err, strings.TrimSpace(string(out)))
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return "", fmt.Errorf("бинарь %s вернул пустую версию", filepath.Base(path))
	}
	return fields[len(fields)-1], nil
}

func removeStage(dir string) {
	if dir == "" {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		oblog.Component("selfupdate").Warn("не удалось очистить каталог обновления", "dir", dir, "err", err)
	}
}

// removeManagedStage ignores paths loaded from state.json unless they are a
// direct child of our updates directory. A damaged state file must never turn
// cleanup into recursive deletion of an arbitrary user directory.
func removeManagedStage(dir string) {
	updates, err := updatesDirPath()
	if err != nil {
		oblog.Component("selfupdate").Warn("не удалось определить каталог обновлений", "err", err)
		return
	}
	absUpdates, err := filepath.Abs(updates)
	if err != nil {
		return
	}
	absDir, err := filepath.Abs(dir)
	if err != nil || filepath.Dir(filepath.Clean(absDir)) != filepath.Clean(absUpdates) {
		oblog.Component("selfupdate").Warn("отклонена очистка staging вне каталога обновлений", "dir", dir)
		return
	}
	name := filepath.Base(absDir)
	if !strings.HasPrefix(name, ".stage-") && !strings.HasPrefix(name, "build-") && !strings.HasPrefix(name, "v") {
		oblog.Component("selfupdate").Warn("отклонена очистка зарезервированного пути в каталоге обновлений", "dir", dir)
		return
	}
	info, err := os.Lstat(absDir)
	if err != nil {
		if !os.IsNotExist(err) {
			oblog.Component("selfupdate").Warn("не удалось проверить staging перед очисткой", "dir", dir, "err", err)
		}
		return
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		oblog.Component("selfupdate").Warn("отклонена очистка staging, который не является обычным каталогом", "dir", dir)
		return
	}
	removeStage(absDir)
}

// sortedNames возвращает имена файлов в порядке PackageBinaries: основной
// бинарь подменяется первым, чтобы при сбое на втором откат был короче.
func sortedNames(files map[string]string) []string {
	var out []string
	for _, name := range PackageBinaries() {
		if files[name] != "" {
			out = append(out, name)
		}
	}
	return out
}
