package metadata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cron "github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

type ScheduledJob struct {
	Name      string            `yaml:"name"`
	Title     string            `yaml:"title"`
	Titles    map[string]string `yaml:"titles"`
	Schedule  string            `yaml:"schedule"`
	Processor string            `yaml:"processor"`
	Params    map[string]any    `yaml:"params"`
	Enabled   bool              `yaml:"enabled"`
	OnError   string            `yaml:"on_error"`
	Timeout   int               `yaml:"timeout"` // seconds
}

// DisplayName возвращает заголовок регламентного задания с учётом языка.
func (j *ScheduledJob) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := j.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if j.Title != "" {
		return j.Title
	}
	return j.Name
}

func LoadScheduledFile(path string) (*ScheduledJob, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("scheduled: read %s: %w", path, err)
	}
	var job ScheduledJob
	if err := yaml.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("scheduled: parse %s: %w", path, err)
	}
	if job.OnError == "" {
		job.OnError = "continue"
	}
	if job.Timeout == 0 {
		job.Timeout = 3600
	}
	if err := ValidateSchedule(job.Schedule); err != nil {
		// Имя файла, а не полный путь: `check` добавляет к сообщению свой
		// префикс с путём и строкой, и полный путь дублировался бы в выводе.
		return nil, fmt.Errorf("задание %s: %w", filepath.Base(path), err)
	}
	return &job, nil
}

// ValidateSchedule разбирает расписание тем же парсером, что и планировщик
// (#965).
//
// Раньше расписание не проверялось нигде, кроме самого планировщика, и
// опечатка в нём проходила `onebase check` со словами «ошибок не найдено», а
// потом не давала серверу запуститься вовсе — падал не сбойный джоб, а вся
// база вместе с UI, REST и остальными заданиями. Проверять здесь важно именно
// потому, что зелёный check — основной сигнал «конфигурацию можно катить».
//
// Парсер тот же: cron.New() внутри использует standardParser, а ParseStandard
// это он и есть. Разойтись они не могут по построению — иначе check снова
// начал бы пропускать то, на чём падает рантайм.
func ValidateSchedule(schedule string) error {
	trimmed := strings.TrimSpace(schedule)
	if trimmed == "" {
		// Пустое расписание — законный способ описать задание, которое
		// запускают только по требованию (кнопкой админки или из кода).
		// Планировщик такое задание в cron просто не заводит.
		return nil
	}
	if _, err := cron.ParseStandard(trimmed); err != nil {
		return fmt.Errorf("расписание %q не разбирается: %w "+
			"(ожидается пять полей «минуты часы день месяц день_недели» либо @every/@daily и т.п.)",
			schedule, err)
	}
	return nil
}

func LoadScheduledDir(dir string) ([]*ScheduledJob, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scheduled: readdir %s: %w", dir, err)
	}
	var jobs []*ScheduledJob
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		job, err := LoadScheduledFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}
