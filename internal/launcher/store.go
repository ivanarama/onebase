package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// Base represents a registered onebase information base.
type Base struct {
	ID           string    `yaml:"id"`
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
}

type storeFile struct {
	Bases []*Base `yaml:"bases"`
}

// Store persists the list of information bases in ~/.onebase/ibases.yaml.
type Store struct {
	path string
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
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dir, "ibases.yaml")}, nil
}

func (s *Store) load() ([]*Base, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f storeFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return f.Bases, nil
}

func (s *Store) save(bases []*Base) error {
	data, err := yaml.Marshal(&storeFile{Bases: bases})
	if err != nil {
		return err
	}
	// 0600: в реестре лежит поле db — строка подключения к PostgreSQL вместе с
	// паролем. При 0644 её мог прочитать любой локальный пользователь машины.
	//
	// Читает реестр только сам лаунчер: серверу параметры подключения приходят
	// аргументами (`onebase run --db ...`), в файл он не смотрит. Поэтому
	// ужесточение никого не отключает.
	//
	// Уже существующие файлы с 0644 чинятся сами: запись идёт во временный
	// файл с новыми правами, и Rename заменяет им прежний.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) List() ([]*Base, error) {
	bases, err := s.load()
	if err != nil {
		return nil, err
	}
	if bases == nil {
		return []*Base{}, nil
	}
	return bases, nil
}

func (s *Store) Get(id string) (*Base, error) {
	bases, err := s.load()
	if err != nil {
		return nil, err
	}
	for _, b := range bases {
		if b.ID == id {
			return b, nil
		}
	}
	return nil, fmt.Errorf("base %q not found", id)
}

func (s *Store) Add(b *Base) error {
	if b.ID == "" {
		b.ID = uuid.New().String()
	}
	if b.Created.IsZero() {
		b.Created = time.Now()
	}
	if b.Port == 0 {
		b.Port = 8080
	}
	if b.ConfigSource == "" {
		b.ConfigSource = "database"
	}
	bases, err := s.load()
	if err != nil {
		return err
	}
	return s.save(append(bases, b))
}

func (s *Store) Update(b *Base) error {
	bases, err := s.load()
	if err != nil {
		return err
	}
	for i, existing := range bases {
		if existing.ID == b.ID {
			bases[i] = b
			return s.save(bases)
		}
	}
	return fmt.Errorf("base %q not found", b.ID)
}

// Move сдвигает базу с заданным id на delta позиций в списке
// (delta=-1 — вверх, delta=+1 — вниз). Сдвиг за границы списка — no-op.
func (s *Store) Move(id string, delta int) error {
	bases, err := s.load()
	if err != nil {
		return err
	}
	idx := -1
	for i, b := range bases {
		if b.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("base %q not found", id)
	}
	target := idx + delta
	if target < 0 || target >= len(bases) {
		return nil
	}
	bases[idx], bases[target] = bases[target], bases[idx]
	return s.save(bases)
}

func (s *Store) Remove(id string) error {
	bases, err := s.load()
	if err != nil {
		return err
	}
	var filtered []*Base
	for _, b := range bases {
		if b.ID != id {
			filtered = append(filtered, b)
		}
	}
	return s.save(filtered)
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
