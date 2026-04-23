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
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
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
