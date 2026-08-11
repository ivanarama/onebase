package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	)
	started, err := updateStateRecovering(func(st *State) error {
		ch = opts.Channel
		if ch == "" {
			ch = st.ChannelOrDefault()
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
		removeStage(obsoleteStage)
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
		removeStage(obsoleteStage)
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
	if !staged.Verified {
		return fmt.Errorf("selfupdate: обновление %s не проверено — применять нельзя", staged.Tag)
	}
	stageLock, err := acquireStageOperationLock()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, stageLock.Unlock()) }()

	prev, err := PrevDir()
	if err != nil {
		return err
	}
	removeStage(prev)
	if err := os.MkdirAll(prev, fsmode.SecretDir); err != nil {
		return err
	}

	type swapped struct{ target, backup string }
	var done []swapped

	rollbackAll := func() {
		for _, s := range done {
			if err := Rollback(s.target, s.backup); err != nil {
				oblog.Component("selfupdate").Error("не удалось вернуть бинарь после сорвавшегося обновления",
					"target", s.target, "err", err)
			}
		}
	}

	for _, name := range staged.Files {
		src := filepath.Join(staged.Dir, name)
		dst := filepath.Join(targetDir, name)
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			// Такого бинаря в установке нет (например, поставили без GUI) —
			// не добавляем его, чтобы обновление не меняло состав установки.
			continue
		}
		backup, err := SwapBinary(dst, src)
		if err != nil {
			rollbackAll()
			return err
		}
		done = append(done, swapped{target: dst, backup: backup})
	}
	if len(done) == 0 {
		return fmt.Errorf("selfupdate: в %s не найдено ни одного бинаря платформы", targetDir)
	}

	// Успех: прежние бинари переезжают в prev, staging больше не нужен.
	for _, s := range done {
		if err := moveFile(s.backup, filepath.Join(prev, filepath.Base(s.target))); err != nil {
			// Откат станет недоступен, но платформа уже обновлена и работает —
			// валить операцию из-за этого нельзя.
			oblog.Component("selfupdate").Warn("прежний бинарь не сохранён для отката", "file", s.backup, "err", err)
		}
	}
	removeStage(staged.Dir)
	return nil
}

// RollbackPrev возвращает бинари предыдущей версии из ~/.onebase/updates/prev.
func RollbackPrev(targetDir string) (resultErr error) {
	stageLock, err := acquireStageOperationLock()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, stageLock.Unlock()) }()

	prev, err := PrevDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(prev)
	if os.IsNotExist(err) {
		return fmt.Errorf("selfupdate: возвращаться не на что — предыдущая версия не сохранена (откат доступен только сразу после обновления)")
	}
	if err != nil {
		return fmt.Errorf("selfupdate: предыдущая версия недоступна: %w", err)
	}
	var restored int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src := filepath.Join(prev, e.Name())
		dst := filepath.Join(targetDir, e.Name())
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			continue
		}
		if err := Rollback(dst, src); err != nil {
			return err
		}
		restored++
	}
	if restored == 0 {
		return fmt.Errorf("selfupdate: в %s нет бинарей для отката", prev)
	}
	// Откат одноразовый: вернувшись на предыдущую версию, «предыдущей» больше
	// нет. Иначе повторный вызов нашёл бы те же файлы и отчитался успехом, хотя
	// откатываться уже некуда.
	removeStage(prev)
	return nil
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
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput() //nolint:gosec // G204: путь к бинарю собран нами (staging или каталог установки), не из пользовательского ввода
	if err != nil {
		return "", fmt.Errorf("не удалось определить версию бинаря %s: %w: %s", filepath.Base(path), err, strings.TrimSpace(string(out)))
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return "", fmt.Errorf("бинарь %s вернул пустую версию", filepath.Base(path))
	}
	return fields[len(fields)-1], nil
}

// moveFile переносит файл, переживая переезд между томами (TEMP и каталог
// установки бывают на разных дисках, и тогда Rename не работает).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	f, err := os.Open(src) //nolint:gosec // G304: путь наш — резервная копия бинаря
	if err != nil {
		return err
	}
	defer oblog.CloseQuiet("selfupdate", "резервную копию", f)
	if err := writeFile(f, dst, 0o755); err != nil {
		return err
	}
	return os.Remove(src)
}

func removeStage(dir string) {
	if dir == "" {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		oblog.Component("selfupdate").Warn("не удалось очистить каталог обновления", "dir", dir, "err", err)
	}
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
