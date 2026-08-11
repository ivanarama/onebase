package selfupdate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ivantit66/onebase/internal/fsmode"
)

// StateFileName — состояние обновлений: что известно о новых версиях, что уже
// скачано, что можно откатить.
//
// Это единственный канал связи с процессами баз: экран «О программе» в
// Предприятии читает файл и НЕ ходит в сеть сам. Иначе каждый рабочий процесс
// начал бы стучаться на GitHub, нарушая и политику исходящих соединений
// (net.enabled), и офлайн-эксплуатацию.
const StateFileName = "state.json"

// stateLockFileName is kept separate from StateFileName: state.json is
// replaced atomically, while every process keeps this stable inode/handle
// locked for the whole read-modify-write transaction.
const stateLockFileName = "state.lock"

// stateMu is required in addition to the OS lock. In particular, POSIX record
// locks are process-scoped and do not serialize goroutines in one process.
var stateMu sync.Mutex

// maxNotesRunes — сколько текста релиза храним. Тело сборки содержит список
// коммитов и растёт; для показа в модалке этого с запасом достаточно.
const maxNotesRunes = 8000

// State — состояние обновлений платформы для текущего пользователя.
type State struct {
	// Channel — выбранный канал (пусто = DefaultChannel).
	Channel Channel `json:"channel,omitempty"`
	// CheckedAt/CheckError — когда последний раз спрашивали GitHub и чем это
	// кончилось. Ошибка хранится, чтобы UI мог сказать «проверить не удалось»
	// вместо тишины.
	CheckedAt  time.Time `json:"checked_at,omitempty"`
	CheckError string    `json:"check_error,omitempty"`
	// CheckGeneration is an internal CAS token. A slower, older network check
	// must not overwrite a newer check that has already completed.
	CheckGeneration uint64 `json:"check_generation,omitempty"`
	// Current — версия платформы, которой делалась проверка.
	Current string `json:"current,omitempty"`
	// Latest — последний известный релиз канала.
	Latest *RelInfo `json:"latest,omitempty"`
	// Staged — скачанное и проверенное обновление, готовое к применению.
	Staged *StagedInfo `json:"staged,omitempty"`
	// Prev — версия, с которой обновились: на неё можно откатиться.
	Prev *RelInfo `json:"prev,omitempty"`
	// RestartBases — базы, работавшие в момент применения обновления. Новый
	// лаунчер поднимает их обратно и очищает список.
	RestartBases []string `json:"restart_bases,omitempty"`
	// AutoApply — применять скачанное обновление при следующем старте лаунчера
	// без вопросов. По умолчанию выключено: на канале build молча менять
	// платформу нельзя.
	AutoApply bool `json:"auto_apply,omitempty"`
}

// RelInfo — сведения о релизе, которые нужны интерфейсу.
type RelInfo struct {
	Tag         string    `json:"tag"`
	PublishedAt time.Time `json:"published_at,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	URL         string    `json:"url,omitempty"`
}

// StagedInfo — что лежит в staging-каталоге.
type StagedInfo struct {
	Tag      string    `json:"tag"`
	Dir      string    `json:"dir"`
	Files    []string  `json:"files,omitempty"`
	Verified bool      `json:"verified"`
	StagedAt time.Time `json:"staged_at,omitempty"`
}

// ChannelOrDefault возвращает выбранный канал либо канал по умолчанию.
func (s State) ChannelOrDefault() Channel {
	switch s.Channel {
	case ChannelStable, ChannelBuild:
		return s.Channel
	default:
		return DefaultChannel
	}
}

// UpdateAvailable сообщает, есть ли что предложить пользователю на current.
func (s State) UpdateAvailable(current string) bool {
	return s.Latest != nil && Newer(current, s.Latest.Tag)
}

// StagedReady сообщает, что обновление скачано, проверено и ждёт применения.
func (s State) StagedReady() bool {
	return s.Staged != nil && s.Staged.Verified && s.Staged.Tag != ""
}

// updatesDirPath возвращает путь ~/.onebase/updates, ничего не создавая.
// Отдельно от UpdatesDir, потому что читателей больше, чем писателей: экран
// «О программе» в Предприятии только смотрит состояние, а процесс базы может
// работать службой под чужим профилем — создавать там каталоги незачем.
func updatesDirPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("selfupdate: домашний каталог: %w", err)
	}
	return filepath.Join(home, ".onebase", "updates"), nil
}

// UpdatesDir возвращает ~/.onebase/updates, создавая его при необходимости.
// Права 0700: каталог одного пользователя, как и реестр баз рядом.
func UpdatesDir() (string, error) {
	dir, err := updatesDirPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, fsmode.SecretDir); err != nil {
		return "", err
	}
	return dir, nil
}

// StageDir возвращает каталог для распакованных бинарей релиза tag.
func StageDir(tag string) (string, error) {
	updates, err := UpdatesDir()
	if err != nil {
		return "", err
	}
	// Тег приходит от GitHub — в путь пускаем только базовое имя, чтобы
	// «../» в теге не увёл запись из каталога обновлений.
	return filepath.Join(updates, filepath.Base(tag)), nil
}

// newStageDir gives each download attempt its own directory. A predictable
// per-tag path lets concurrent Fetch calls delete or overwrite each other's
// verified files before either one publishes its State.Staged record.
func newStageDir() (string, error) {
	updates, err := UpdatesDir()
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(updates, ".stage-*")
}

// PrevDir возвращает каталог, куда уезжает предыдущая версия бинарей.
func PrevDir() (string, error) {
	updates, err := UpdatesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(updates, "prev"), nil
}

// LoadState читает состояние. Битый или недоступный файл — не повод падать:
// возвращается пустое состояние и ошибка, которую вызывающий волен только
// записать в журнал (лаунчер обязан подняться в любом случае).
func LoadState() (state State, resultErr error) {
	stateMu.Lock()
	defer stateMu.Unlock()

	updates, err := updatesDirPath()
	if err != nil {
		return State{}, err
	}
	// Preserve the read-only behaviour for a new profile: merely looking at the
	// update state must not create ~/.onebase/updates.
	if _, err := os.Stat(updates); os.IsNotExist(err) {
		return State{}, nil
	} else if err != nil {
		return State{}, err
	}
	lockPath := filepath.Join(updates, stateLockFileName)
	lock, err := acquireStateReadLock(lockPath)
	if err != nil {
		return State{}, err
	}
	if lock == nil {
		// Profiles written by older OneBase versions do not have a lock file.
		// Read once, then retry under the lock if a current writer created it in
		// the meantime. Thus LoadState itself remains side-effect free.
		state, loadErr := loadStateFile(filepath.Join(updates, StateFileName))
		lock, err = acquireStateReadLock(lockPath)
		if err != nil {
			return State{}, errors.Join(loadErr, err)
		}
		if lock == nil {
			return state, loadErr
		}
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Unlock()) }()
	return loadStateFile(filepath.Join(updates, StateFileName))
}

func loadStateFile(path string) (State, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is the fixed update-state file
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, &invalidStateError{err: err}
	}
	return s, nil
}

// SaveState атомарно полностью перезаписывает состояние. Для изменения полей
// на основе текущего состояния вызывающий должен использовать UpdateState.
func SaveState(s State) (resultErr error) {
	stateMu.Lock()
	defer stateMu.Unlock()

	updates, err := UpdatesDir()
	if err != nil {
		return err
	}
	lock, err := acquireStateFileLock(filepath.Join(updates, stateLockFileName))
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Unlock()) }()
	return saveStateFile(filepath.Join(updates, StateFileName), s)
}

// UpdateState changes selected state fields transactionally. The callback is
// called after the latest on-disk state has been read and while both the
// process mutex and the cross-process file lock are held. It must be quick,
// must not perform blocking I/O, and must not call back into this package.
//
// When mutate returns an error, the file is left unchanged.
func UpdateState(mutate func(*State) error) (State, error) {
	return updateState(false, mutate)
}

// updateStateRecovering is used by background update checks. A malformed JSON
// file has no fields that can be preserved safely, so these paths retain the
// historic behaviour of replacing it with a fresh state. I/O and lock errors
// remain fail-closed.
func updateStateRecovering(mutate func(*State) error) (State, error) {
	return updateState(true, mutate)
}

func updateState(recoverInvalid bool, mutate func(*State) error) (result State, resultErr error) {
	if mutate == nil {
		return State{}, errors.New("selfupdate: state mutation is nil")
	}
	stateMu.Lock()
	defer stateMu.Unlock()

	updates, err := UpdatesDir()
	if err != nil {
		return State{}, err
	}
	lock, err := acquireStateFileLock(filepath.Join(updates, stateLockFileName))
	if err != nil {
		return State{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Unlock()) }()

	path := filepath.Join(updates, StateFileName)
	state, loadErr := loadStateFile(path)
	if loadErr != nil {
		var invalid *invalidStateError
		if !recoverInvalid || !errors.As(loadErr, &invalid) {
			return State{}, loadErr
		}
		state = State{}
	}
	original := cloneState(state)
	if err := mutate(&state); err != nil {
		return original, err
	}
	writeErr := saveStateFile(path, state)
	return state, writeErr
}

func cloneState(s State) State {
	if s.Latest != nil {
		latest := *s.Latest
		s.Latest = &latest
	}
	if s.Staged != nil {
		staged := *s.Staged
		staged.Files = append([]string(nil), s.Staged.Files...)
		s.Staged = &staged
	}
	if s.Prev != nil {
		prev := *s.Prev
		s.Prev = &prev
	}
	s.RestartBases = append([]string(nil), s.RestartBases...)
	return s
}

func saveStateFile(path string, s State) error {
	if s.Latest != nil {
		// Do not mutate a RelInfo owned by the caller while normalising the
		// persisted copy.
		latest := *s.Latest
		s.Latest = &latest
		s.Latest.Notes = trimRunes(s.Latest.Notes, maxNotesRunes)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(bytes.NewReader(data), path, fsmode.File)
}

type invalidStateError struct{ err error }

func (e *invalidStateError) Error() string {
	return fmt.Sprintf("selfupdate: состояние обновлений не разобрано: %v", e.err)
}

func (e *invalidStateError) Unwrap() error { return e.err }

// trimRunes обрезает строку по границе рун, а не байтов: обрыв UTF-8 посреди
// символа превратил бы JSON в мусор при показе.
func trimRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
