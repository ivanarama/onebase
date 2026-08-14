package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/fsmode"
	"gopkg.in/yaml.v3"
)

// Base represents a registered onebase information base.
type Base struct {
	ID string `yaml:"id"`
	// ControlToken is a persistent 256-bit launcher lifecycle-control secret.
	// It is safe to persist here because ibases.yaml is always written as 0600.
	ControlToken string    `yaml:"control_token,omitempty"`
	Name         string    `yaml:"name"`
	ConfigSource string    `yaml:"config_source"` // "file" or "database"
	Path         string    `yaml:"path,omitempty"`
	DB           string    `yaml:"db"`
	Port         int       `yaml:"port"`
	Created      time.Time `yaml:"created"`
	LastOpened   time.Time `yaml:"last_opened,omitempty"`

	// DBType selects the database engine: "postgres" (default, uses DB DSN)
	// or "sqlite" (uses DBPath to point at a .db file).
	DBType string `yaml:"db_type,omitempty"`
	// DBPath is the SQLite database file path (only when DBType="sqlite").
	DBPath string `yaml:"db_path,omitempty"`
	// Host — интерфейс прослушивания дочернего процесса базы (issue #590).
	// Пусто/"127.0.0.1" — только этот компьютер (умолчание, secure-by-default);
	// "0.0.0.0" — все интерфейсы, база доступна из локальной сети.
	Host string `yaml:"host,omitempty"`
}

// LauncherSettings — настройки самого лаунчера (не базы), лежат в том же
// ibases.yaml рядом со списком баз.
type LauncherSettings struct {
	// OnClose — что делать при закрытии окна информационных баз, когда базы
	// работают: OnCloseAsk (спросить, по умолчанию), OnCloseBackground
	// (оставить работать), OnCloseStop (остановить все). См. closepolicy.go.
	OnClose string `yaml:"on_close,omitempty"`
}

// Store persists the list of information bases in ~/.onebase/ibases.yaml.
type Store struct {
	path string
}

// OS file-lock ownership semantics differ across supported platforms. A
// package-wide mutex guarantees coordination between every Store instance in
// this process; the file lock coordinates distinct launcher processes.
var storeProcessMu sync.RWMutex

var ErrBaseNotFound = errors.New("launcher: base not found")

// BasePortConflictError is returned from the same transaction that would write
// a registration. Handler-level List checks provide friendly early feedback,
// but only this error closes the check/write race across goroutines and across
// launcher processes.
type BasePortConflictError struct {
	Port          int
	OwnerID       string
	OwnerName     string
	SuggestedPort int
}

func (e *BasePortConflictError) Error() string {
	return fmt.Sprintf("launcher store: port %d is already registered to base %q", e.Port, e.OwnerID)
}

func NewStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("launcher: home dir: %w", err)
	}
	dir := filepath.Join(home, ".onebase")
	// 0700, а не 0755: каталог принадлежит одному пользователю — сервис,
	// установленный через `onebase service install --user`, работает со своим
	// HOME и сюда не заглядывает. Существующему каталогу MkdirAll права не
	// меняет, поэтому на уже развёрнутых машинах это ничего не ломает.
	if err := os.MkdirAll(dir, fsmode.SecretDir); err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dir, "ibases.yaml")}, nil
}

// readDocument читает реестр как YAML-дерево. Неизвестные ключи и комментарии
// остаются в дереве и переживают точечные изменения настроек.
//
// Вызывающий обязан держать storeProcessMu; для mutation — ещё и
// межпроцессный lock.
func (s *Store) readDocument() (*yaml.Node, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return newStoreDocument(), nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return newStoreDocument(), nil
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("launcher store: разрешён ровно один YAML-документ")
		}
		return nil, err
	}
	if _, err := storeDocumentMapping(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func newStoreDocument() *yaml.Node {
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
	}}}
}

func storeDocumentMapping(doc *yaml.Node) (*yaml.Node, error) {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 ||
		doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("launcher store: ожидалось YAML-отображение в корне")
	}
	return doc.Content[0], nil
}

// storeMappingValue находит ровно один строковый ключ. Дубликат управляемого
// ключа — ошибка: молча изменить первый и оставить второй означало бы записать
// неоднозначный реестр.
func storeMappingValue(mapping *yaml.Node, key string) (*yaml.Node, bool, error) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("launcher store: ключ %q находится не в отображении", key)
	}
	var found *yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Kind != yaml.ScalarNode || k.Value != key {
			continue
		}
		if found != nil {
			return nil, false, fmt.Errorf("launcher store: ключ %q указан несколько раз", key)
		}
		found = mapping.Content[i+1]
	}
	return found, found != nil, nil
}

func setStoreMappingValue(mapping *yaml.Node, key string, value *yaml.Node) error {
	if _, _, err := storeMappingValue(mapping, key); err != nil {
		return err
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if k := mapping.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			mapping.Content[i+1] = value
			return nil
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	return nil
}

func removeStoreMappingValue(mapping *yaml.Node, key string) error {
	if _, _, err := storeMappingValue(mapping, key); err != nil {
		return err
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if k := mapping.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return nil
		}
	}
	return nil
}

func storeBasesSequence(doc *yaml.Node, create bool) (*yaml.Node, error) {
	root, err := storeDocumentMapping(doc)
	if err != nil {
		return nil, err
	}
	bases, ok, err := storeMappingValue(root, "bases")
	if err != nil {
		return nil, err
	}
	if !ok {
		if !create {
			return nil, nil
		}
		bases = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if err := setStoreMappingValue(root, "bases", bases); err != nil {
			return nil, err
		}
	} else if bases.Kind == yaml.ScalarNode && bases.Tag == "!!null" {
		if !create {
			return nil, nil
		}
		bases.Kind = yaml.SequenceNode
		bases.Tag = "!!seq"
		bases.Value = ""
		bases.Content = nil
	} else if bases.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("launcher store: bases должен быть последовательностью")
	}
	return bases, nil
}

func decodeBaseNode(node *yaml.Node) (*Base, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("launcher store: элемент bases должен быть отображением")
	}
	var base Base
	if err := node.Decode(&base); err != nil {
		return nil, err
	}
	return &base, nil
}

var managedBaseYAMLKeys = []string{
	"id", "control_token", "name", "config_source", "path", "db", "port",
	"created", "last_opened", "db_type", "db_path", "host",
}

// patchBaseNode updates only fields owned by the current Base schema. Unknown
// fields and comments written by a newer launcher stay attached to the same
// mapping instead of disappearing on an ordinary open/reorder/edit.
func patchBaseNode(target *yaml.Node, base *Base) error {
	var encoded yaml.Node
	if err := encoded.Encode(base); err != nil {
		return err
	}
	if encoded.Kind != yaml.MappingNode {
		return fmt.Errorf("launcher store: Base encoded as non-mapping")
	}
	for _, key := range managedBaseYAMLKeys {
		newValue, exists, err := storeMappingValue(&encoded, key)
		if err != nil {
			return err
		}
		oldValue, oldExists, err := storeMappingValue(target, key)
		if err != nil {
			return err
		}
		if !exists {
			if oldExists {
				if err := removeStoreMappingValue(target, key); err != nil {
					return err
				}
			}
			continue
		}
		if oldExists {
			// Preserve comments/style from the user's YAML around a managed value.
			newValue.HeadComment = oldValue.HeadComment
			newValue.LineComment = oldValue.LineComment
			newValue.FootComment = oldValue.FootComment
			if newValue.Kind == yaml.ScalarNode {
				newValue.Style = oldValue.Style
			}
		}
		if err := setStoreMappingValue(target, key, newValue); err != nil {
			return err
		}
	}
	return nil
}

func decodeStoreBases(doc *yaml.Node) ([]*Base, error) {
	root, err := storeDocumentMapping(doc)
	if err != nil {
		return nil, err
	}
	basesNode, ok, err := storeMappingValue(root, "bases")
	if err != nil {
		return nil, err
	}
	if !ok || basesNode.Kind == yaml.ScalarNode && basesNode.Tag == "!!null" {
		return []*Base{}, nil
	}
	var bases []*Base
	if err := basesNode.Decode(&bases); err != nil {
		return nil, fmt.Errorf("launcher store: bases: %w", err)
	}
	if bases == nil {
		bases = []*Base{}
	}
	if err := validateStoreBases(bases); err != nil {
		return nil, err
	}
	return bases, nil
}

func validateStoreBases(bases []*Base) error {
	seen := make(map[string]struct{}, len(bases))
	for i, base := range bases {
		if base == nil {
			return fmt.Errorf("launcher store: bases[%d] is null", i)
		}
		if base.ID == "" {
			return fmt.Errorf("launcher store: bases[%d] has an empty id", i)
		}
		if _, duplicate := seen[base.ID]; duplicate {
			return fmt.Errorf("launcher store: base %q is listed more than once", base.ID)
		}
		seen[base.ID] = struct{}{}
	}
	return nil
}

func setStoreBases(doc *yaml.Node, bases []*Base) error {
	root, err := storeDocumentMapping(doc)
	if err != nil {
		return err
	}
	if bases == nil {
		bases = []*Base{}
	}
	var value yaml.Node
	if err := value.Encode(bases); err != nil {
		return err
	}
	return setStoreMappingValue(root, "bases", &value)
}

// mutateDocument выполняет полную mutation-транзакцию. RWMutex сериализует
// горутины одного Store, а OS-lock — разные Store и разные launcher-процессы.
// Важно держать оба lock от чтения исходного дерева до Rename: отдельные lock
// вокруг read/write всё равно оставили бы lost-update окно.
func (s *Store) mutateDocument(edit func(*yaml.Node) (bool, error)) (err error) {
	storeProcessMu.Lock()
	defer storeProcessMu.Unlock()

	fileLock, err := acquireStoreFileLock(s.path + ".lock")
	if err != nil {
		return err
	}
	defer func() {
		if unlockErr := fileLock.Unlock(); unlockErr != nil {
			err = errors.Join(err, unlockErr)
		}
	}()

	doc, err := s.readDocument()
	if err != nil {
		return err
	}
	changed, err := edit(doc)
	if err != nil || !changed {
		return err
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return writeStoreFileAtomic(s.path, data)
}

func (s *Store) mutateBases(edit func([]*Base) ([]*Base, bool, error)) error {
	return s.mutateDocument(func(doc *yaml.Node) (bool, error) {
		bases, err := decodeStoreBases(doc)
		if err != nil {
			return false, err
		}
		bases, changed, err := edit(bases)
		if err != nil || !changed {
			return false, err
		}
		return true, setStoreBases(doc, bases)
	})
}

// stageStoreFile записывает и синхронизирует уникальный O_EXCL temp в том же
// каталоге. Отдельная стадия делает свойства temp проверяемыми без test hooks.
// Получивший имя обязан либо Rename, либо удалить файл.
func stageStoreFile(path string, data []byte) (name string, err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", err
	}
	name = tmp.Name()
	tmpName := name
	defer func() {
		if err != nil {
			if removeErr := os.Remove(tmpName); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, removeErr)
			}
		}
	}()

	if chmodErr := tmp.Chmod(fsmode.SecretFile); chmodErr != nil {
		return "", errors.Join(chmodErr, tmp.Close())
	}
	n, writeErr := tmp.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		return "", errors.Join(writeErr, tmp.Close())
	}
	if syncErr := tmp.Sync(); syncErr != nil {
		return "", errors.Join(syncErr, tmp.Close())
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return "", closeErr
	}
	return name, nil
}

func writeStoreFileAtomic(path string, data []byte) (err error) {
	tmp, err := stageStoreFile(path, data)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			if removeErr := os.Remove(tmp); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, removeErr)
			}
		}
	}()
	if err = replaceStoreFile(tmp, path); err != nil {
		return err
	}
	return syncStoreDirectory(filepath.Dir(path))
}

func syncStoreDirectory(dir string) error {
	f, err := os.Open(dir) //nolint:gosec // parent of the caller-owned store path
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	// Windows does not consistently support flushing an opened directory. The
	// staged file itself was flushed before Rename; Unix additionally requires
	// the directory entry to be durable before the transaction lock is released.
	if runtime.GOOS == "windows" {
		syncErr = nil
	}
	return errors.Join(syncErr, closeErr)
}

// Snapshot возвращает список баз и настройки из одного чтения файла.
func (s *Store) Snapshot() (bases []*Base, settings LauncherSettings, resultErr error) {
	storeProcessMu.RLock()
	defer storeProcessMu.RUnlock()
	fileLock, err := acquireStoreFileLock(s.path + ".lock")
	if err != nil {
		return nil, LauncherSettings{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, fileLock.Unlock()) }()

	doc, err := s.readDocument()
	if err != nil {
		return nil, LauncherSettings{}, err
	}
	bases, err = decodeStoreBases(doc)
	if err != nil {
		return nil, LauncherSettings{}, err
	}
	root, err := storeDocumentMapping(doc)
	if err != nil {
		return nil, LauncherSettings{}, err
	}
	settingsNode, exists, err := storeMappingValue(root, "settings")
	if err != nil {
		return nil, LauncherSettings{}, err
	}
	if exists && (settingsNode.Kind != yaml.ScalarNode || settingsNode.Tag != "!!null") {
		if settingsNode.Kind != yaml.MappingNode {
			return nil, LauncherSettings{}, fmt.Errorf("launcher store: settings must be a mapping")
		}
		if err := settingsNode.Decode(&settings); err != nil {
			return nil, LauncherSettings{}, err
		}
	}
	return bases, settings, nil
}

// withBaseReadLock reads one base and keeps both the in-process and
// cross-process Store locks held while use runs. This is intentionally more
// restrictive than Get: callers that must act on the exact record they read
// can close the check/use race with a concurrent Update, Remove, or Add.
//
// use must not call another Store method; Store locks are not re-entrant.
func (s *Store) withBaseReadLock(id string, use func(*Base) error) (resultErr error) {
	if use == nil {
		return errors.New("launcher store: nil base callback")
	}
	storeProcessMu.RLock()
	defer storeProcessMu.RUnlock()

	fileLock, err := acquireStoreFileLock(s.path + ".lock")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fileLock.Unlock()) }()

	doc, err := s.readDocument()
	if err != nil {
		return err
	}
	bases, err := decodeStoreBases(doc)
	if err != nil {
		return err
	}
	var found *Base
	for _, base := range bases {
		if base == nil || base.ID != id {
			continue
		}
		if found != nil {
			return fmt.Errorf("launcher store: base %q is listed more than once", id)
		}
		found = base
	}
	if found == nil {
		return fmt.Errorf("%w: %q", ErrBaseNotFound, id)
	}
	return use(found)
}

// save заменяет только managed-блок bases и сохраняет неизвестные
// top-level/settings ключи исходного YAML. Оставлен как helper для тестов.
func (s *Store) save(bases []*Base) error {
	return s.mutateBases(func([]*Base) ([]*Base, bool, error) {
		return bases, true, nil
	})
}

// Settings возвращает настройки лаунчера (нулевые, если файла ещё нет).
func (s *Store) Settings() (LauncherSettings, error) {
	_, settings, err := s.Snapshot()
	return settings, err
}

// SetOnClose точечно меняет settings.on_close, не декодируя и не пересобирая
// остальные части YAML.
func (s *Store) SetOnClose(policy string) error {
	return s.mutateDocument(func(doc *yaml.Node) (bool, error) {
		root, err := storeDocumentMapping(doc)
		if err != nil {
			return false, err
		}
		settings, ok, err := storeMappingValue(root, "settings")
		if err != nil {
			return false, err
		}
		if !ok {
			settings = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			if err := setStoreMappingValue(root, "settings", settings); err != nil {
				return false, err
			}
		} else if settings.Kind == yaml.ScalarNode && settings.Tag == "!!null" {
			settings.Kind = yaml.MappingNode
			settings.Tag = "!!map"
			settings.Value = ""
			settings.Content = nil
		} else if settings.Kind != yaml.MappingNode {
			return false, fmt.Errorf("launcher store: settings должен быть отображением")
		}

		onClose, exists, err := storeMappingValue(settings, "on_close")
		if err != nil {
			return false, err
		}
		if !exists {
			onClose = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: policy}
			if err := setStoreMappingValue(settings, "on_close", onClose); err != nil {
				return false, err
			}
			return true, nil
		}
		style := onClose.Style
		if onClose.Kind != yaml.ScalarNode {
			style = 0
		}
		onClose.Kind = yaml.ScalarNode
		onClose.Tag = "!!str"
		onClose.Value = policy
		onClose.Style = style
		onClose.Content = nil
		onClose.Alias = nil
		return true, nil
	})
}

// EnsureControlToken возвращает persistent launcher secret базы и создаёт его,
// если база была зарегистрирована до появления control_token. Поиск и точечная
// запись выполняются в одной межпроцессной mutation-транзакции: параллельные
// launcher-процессы не выпустят два разных токена и не перезапишут stale Base.
func (s *Store) EnsureControlToken(id string) (string, error) {
	var token string
	err := s.mutateDocument(func(doc *yaml.Node) (bool, error) {
		root, err := storeDocumentMapping(doc)
		if err != nil {
			return false, err
		}
		bases, ok, err := storeMappingValue(root, "bases")
		if err != nil {
			return false, err
		}
		if !ok || bases.Kind != yaml.SequenceNode {
			return false, fmt.Errorf("base %q not found", id)
		}

		var target *yaml.Node
		for _, candidate := range bases.Content {
			if candidate.Kind != yaml.MappingNode {
				return false, fmt.Errorf("launcher store: элемент bases должен быть отображением")
			}
			idNode, exists, err := storeMappingValue(candidate, "id")
			if err != nil {
				return false, err
			}
			if !exists {
				continue
			}
			var candidateID string
			if err := idNode.Decode(&candidateID); err != nil {
				return false, fmt.Errorf("launcher store: base id: %w", err)
			}
			if candidateID != id {
				continue
			}
			if target != nil {
				return false, fmt.Errorf("launcher store: base %q указан несколько раз", id)
			}
			target = candidate
		}
		if target == nil {
			return false, fmt.Errorf("base %q not found", id)
		}

		tokenNode, exists, err := storeMappingValue(target, "control_token")
		if err != nil {
			return false, err
		}
		if exists {
			if err := tokenNode.Decode(&token); err != nil {
				return false, fmt.Errorf("launcher store: control_token: %w", err)
			}
			if token != "" {
				return false, nil
			}
		}

		generated, err := generateDebugToken()
		if err != nil {
			return false, err
		}
		value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: generated}
		if err := setStoreMappingValue(target, "control_token", value); err != nil {
			return false, err
		}
		token = generated
		return true, nil
	})
	return token, err
}

func (s *Store) List() ([]*Base, error) {
	bases, _, err := s.Snapshot()
	return bases, err
}

func (s *Store) Get(id string) (*Base, error) {
	bases, _, err := s.Snapshot()
	if err != nil {
		return nil, err
	}
	for _, b := range bases {
		if b.ID == id {
			return b, nil
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrBaseNotFound, id)
}

func (s *Store) Add(b *Base) error {
	if b.ID == "" {
		b.ID = uuid.New().String()
	}
	if b.Created.IsZero() {
		b.Created = time.Now()
	}
	if b.ConfigSource == "" {
		b.ConfigSource = "database"
	}
	return s.mutateDocument(func(doc *yaml.Node) (bool, error) {
		bases, err := storeBasesSequence(doc, true)
		if err != nil {
			return false, err
		}
		registered := make([]*Base, 0, len(bases.Content))
		for _, candidate := range bases.Content {
			existing, err := decodeBaseNode(candidate)
			if err != nil {
				return false, err
			}
			if existing.ID == b.ID {
				return false, fmt.Errorf("launcher store: base %q is already registered", b.ID)
			}
			registered = append(registered, existing)
		}
		// An omitted port is an allocation request, not an implicit request for a
		// duplicate 8080. Choose it while holding the same cross-process lock.
		if b.Port == 0 {
			b.Port = freeRegistryPort(registered)
		}
		if owner := portOwner(registered, b.ID, b.Port); owner != nil {
			return false, &BasePortConflictError{
				Port: b.Port, OwnerID: owner.ID, OwnerName: owner.Name,
				SuggestedPort: freeRegistryPort(registered),
			}
		}
		var value yaml.Node
		if err := value.Encode(b); err != nil {
			return false, err
		}
		bases.Content = append(bases.Content, &value)
		return true, nil
	})
}

func (s *Store) Update(b *Base) error {
	return s.mutateDocument(func(doc *yaml.Node) (bool, error) {
		bases, err := storeBasesSequence(doc, false)
		if err != nil {
			return false, err
		}
		var target *yaml.Node
		var existing *Base
		registered := make([]*Base, 0)
		if bases != nil {
			registered = make([]*Base, 0, len(bases.Content))
			for _, candidate := range bases.Content {
				decoded, err := decodeBaseNode(candidate)
				if err != nil {
					return false, err
				}
				registered = append(registered, decoded)
				if decoded.ID != b.ID {
					continue
				}
				if target != nil {
					return false, fmt.Errorf("launcher store: base %q указан несколько раз", b.ID)
				}
				target, existing = candidate, decoded
			}
		}
		if target == nil {
			return false, fmt.Errorf("base %q not found", b.ID)
		}
		// Preserve the ability to repair or rename a legacy registry that already
		// contains duplicates, but reject every newly introduced collision.
		if b.Port != existing.Port {
			if owner := portOwner(registered, b.ID, b.Port); owner != nil {
				return false, &BasePortConflictError{
					Port: b.Port, OwnerID: owner.ID, OwnerName: owner.Name,
					SuggestedPort: freeRegistryPort(registered),
				}
			}
		}
		updated := b
		if updated.ControlToken == "" && existing.ControlToken != "" {
			copyWithToken := *updated
			copyWithToken.ControlToken = existing.ControlToken
			updated = &copyWithToken
		}
		return true, patchBaseNode(target, updated)
	})
}

// TouchLastOpened updates only the launch timestamp from the latest on-disk
// node. A stale Base captured before a concurrent edit must not revert name,
// port, DSN or other managed fields merely to save this best-effort marker.
func (s *Store) TouchLastOpened(id string, at time.Time) error {
	return s.mutateDocument(func(doc *yaml.Node) (bool, error) {
		bases, err := storeBasesSequence(doc, false)
		if err != nil {
			return false, err
		}
		var target *yaml.Node
		var latest *Base
		if bases != nil {
			for _, candidate := range bases.Content {
				decoded, err := decodeBaseNode(candidate)
				if err != nil {
					return false, err
				}
				if decoded.ID != id {
					continue
				}
				if target != nil {
					return false, fmt.Errorf("launcher store: base %q указан несколько раз", id)
				}
				target, latest = candidate, decoded
			}
		}
		if target == nil {
			return false, fmt.Errorf("%w: %q", ErrBaseNotFound, id)
		}
		latest.LastOpened = at
		return true, patchBaseNode(target, latest)
	})
}

// Move сдвигает базу с заданным id на delta позиций в списке
// (delta=-1 — вверх, delta=+1 — вниз). Сдвиг за границы списка — no-op.
func (s *Store) Move(id string, delta int) error {
	return s.mutateDocument(func(doc *yaml.Node) (bool, error) {
		bases, err := storeBasesSequence(doc, false)
		if err != nil {
			return false, err
		}
		idx := -1
		if bases != nil {
			for i, node := range bases.Content {
				base, err := decodeBaseNode(node)
				if err != nil {
					return false, err
				}
				if base.ID == id {
					if idx >= 0 {
						return false, fmt.Errorf("launcher store: base %q указан несколько раз", id)
					}
					idx = i
				}
			}
		}
		if idx < 0 {
			return false, fmt.Errorf("base %q not found", id)
		}
		target := idx + delta
		if target < 0 || target >= len(bases.Content) {
			return false, nil
		}
		bases.Content[idx], bases.Content[target] = bases.Content[target], bases.Content[idx]
		return true, nil
	})
}

func (s *Store) Remove(id string) error {
	return s.mutateDocument(func(doc *yaml.Node) (bool, error) {
		bases, err := storeBasesSequence(doc, false)
		if err != nil {
			return false, err
		}
		if bases == nil {
			return false, nil
		}
		filtered := make([]*yaml.Node, 0, len(bases.Content))
		changed := false
		for _, node := range bases.Content {
			base, err := decodeBaseNode(node)
			if err != nil {
				return false, err
			}
			if base.ID == id {
				changed = true
				continue
			}
			filtered = append(filtered, node)
		}
		if changed {
			bases.Content = filtered
		}
		return changed, nil
	})
}

// OnebasePath returns the ~/.onebase directory path.
func OnebasePath(sub ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	parts := append([]string{home, ".onebase"}, sub...)
	return filepath.Join(parts...), nil
}
