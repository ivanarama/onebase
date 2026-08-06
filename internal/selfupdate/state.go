package selfupdate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
func LoadState() (State, error) {
	updates, err := updatesDirPath()
	if err != nil {
		return State{}, err
	}
	data, err := os.ReadFile(filepath.Join(updates, StateFileName)) //nolint:gosec // G304: путь фиксирован — файл состояния в каталоге обновлений
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("selfupdate: состояние обновлений не разобрано: %w", err)
	}
	return s, nil
}

// SaveState атомарно записывает состояние.
func SaveState(s State) error {
	updates, err := UpdatesDir()
	if err != nil {
		return err
	}
	if s.Latest != nil {
		s.Latest.Notes = trimRunes(s.Latest.Notes, maxNotesRunes)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(bytes.NewReader(data), filepath.Join(updates, StateFileName), fsmode.File)
}

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
