// Package extform реализует «внешний контур» расширяемости — артефакты,
// которые живут вне версионируемой конфигурации проекта и хранятся в самой
// базе. Их можно загружать/выключать/удалять в рантайме под правом
// администратора, не пересобирая и не передеплоя конфигурацию (расширяемость
// без форка), а также переносить между базами бандлом *.obform.
//
// Контур включает:
//   - печатные формы (_ext_printforms, repo.go) — декларативный YAML, read-only;
//   - отчёты (_ext_reports, repo_reports.go) — только запросы, валидируются;
//   - обработки (_ext_processors, repo_processors.go) — исполняют DSL-код.
//
// Обработки исполняют произвольный код с полными правами интерпретатора,
// поэтому загружать их может только администратор, а запускать — администратор
// либо (если админ пометил обработку доверенной) обычные пользователи. Это
// согласуется с уже существующей админской консолью кода. Внешние .os-печатные
// формы (DSL-рендер) в контур пока не входят.
package extform

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/storage"
)

// Record — одна запись внешней печатной формы.
type Record struct {
	ID         string
	Document   string
	Name       string
	Content    []byte // «голый» YAML printform.PrintForm
	Enabled    bool
	Author     string
	Version    string
	UploadedBy string
	UploadedAt time.Time
}

type Repo struct {
	db *storage.DB
}

func New(db *storage.DB) *Repo {
	return &Repo{db: db}
}

// EnsureSchema создаёт таблицу _ext_printforms, если её ещё нет. Вызывается
// в точках запуска базы рядом с configdb.EnsureSchema.
func (r *Repo) EnsureSchema(ctx context.Context) error {
	d := r.db.Dialect()
	_, err := r.db.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _ext_printforms (
			id          %s PRIMARY KEY,
			document    %s NOT NULL,
			name        %s NOT NULL,
			content     %s NOT NULL,
			enabled     %s NOT NULL,
			author      %s,
			version     %s,
			uploaded_by %s,
			uploaded_at %s NOT NULL DEFAULT %s,
			UNIQUE(document, name)
		)`,
		d.TypeText(), d.TypeText(), d.TypeText(), d.TypeBytes(),
		d.TypeBool(),
		d.TypeText(), d.TypeText(), d.TypeText(),
		d.TypeTimestamp(), d.CurrentTimestampTZ()))
	if err != nil {
		return fmt.Errorf("extform: create table: %w", err)
	}
	return nil
}

// Save вставляет или обновляет внешнюю форму по ключу (document, name).
// Включённость при загрузке выставляется в true — форму только что загрузил
// администратор, логично сразу её показать.
func (r *Repo) Save(ctx context.Context, rec *Record) error {
	d := r.db.Dialect()
	if rec.ID == "" {
		rec.ID = uuid.NewString()
	}
	_, err := r.db.Exec(ctx, fmt.Sprintf(`
		INSERT INTO _ext_printforms (id, document, name, content, enabled, author, version, uploaded_by, uploaded_at)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
		ON CONFLICT (document, name) DO UPDATE SET
			content = EXCLUDED.content,
			enabled = EXCLUDED.enabled,
			author = EXCLUDED.author,
			version = EXCLUDED.version,
			uploaded_by = EXCLUDED.uploaded_by,
			uploaded_at = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4),
		d.Placeholder(5), d.Placeholder(6), d.Placeholder(7), d.Placeholder(8), d.Now(),
		d.Now()),
		rec.ID, rec.Document, rec.Name, rec.Content, true, rec.Author, rec.Version, rec.UploadedBy)
	if err != nil {
		return fmt.Errorf("extform: save: %w", err)
	}
	return nil
}

// List возвращает все внешние формы, отсортированные по (document, name).
func (r *Repo) List(ctx context.Context) ([]*Record, error) {
	return r.query(ctx, "")
}

// ListEnabled возвращает только включённые формы.
func (r *Repo) ListEnabled(ctx context.Context) ([]*Record, error) {
	return r.query(ctx, " WHERE enabled = "+r.db.Dialect().Placeholder(1), true)
}

func (r *Repo) query(ctx context.Context, where string, args ...any) ([]*Record, error) {
	rows, err := r.db.Query(ctx, `SELECT id, document, name, content, enabled, author, version, uploaded_by, uploaded_at FROM _ext_printforms`+where+` ORDER BY document, name`, args...)
	if err != nil {
		return nil, fmt.Errorf("extform: list: %w", err)
	}
	defer rows.Close()
	var out []*Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Get возвращает одну запись по id.
func (r *Repo) Get(ctx context.Context, id string) (*Record, error) {
	rows, err := r.db.Query(ctx, `SELECT id, document, name, content, enabled, author, version, uploaded_by, uploaded_at FROM _ext_printforms WHERE id = `+r.db.Dialect().Placeholder(1), id)
	if err != nil {
		return nil, fmt.Errorf("extform: get: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, fmt.Errorf("extform: форма не найдена: %s", id)
	}
	return scanRecord(rows)
}

// SetEnabled включает/выключает форму.
func (r *Repo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	d := r.db.Dialect()
	_, err := r.db.Exec(ctx,
		`UPDATE _ext_printforms SET enabled = `+d.Placeholder(1)+` WHERE id = `+d.Placeholder(2),
		enabled, id)
	if err != nil {
		return fmt.Errorf("extform: set enabled: %w", err)
	}
	return nil
}

// Delete удаляет форму по id.
func (r *Repo) Delete(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM _ext_printforms WHERE id = `+r.db.Dialect().Placeholder(1), id)
	if err != nil {
		return fmt.Errorf("extform: delete: %w", err)
	}
	return nil
}

// LoadEnabledPrintForms читает все включённые формы, сниффит формат содержимого
// (план 64, этап 4) и разводит их по двум спискам:
//   - legacy — устаревший плоский YAML (printform.PrintForm); конвертируется в
//     макет v2 на загрузке в реестре (reg.SetExternalPrintForms → ConvertLegacy);
//   - layout — декларативный макет v2 (printform.LayoutForm с top-level areas:);
//     регистрируется как есть (reg.SetExternalLayoutForms).
//
// Записи с непарсящимся содержимым пропускаются с предупреждением, чтобы одна
// битая форма не валила загрузку остальных.
func (r *Repo) LoadEnabledPrintForms(ctx context.Context) ([]*printform.PrintForm, []*printform.LayoutForm, error) {
	recs, err := r.ListEnabled(ctx)
	if err != nil {
		return nil, nil, err
	}
	var legacy []*printform.PrintForm
	var layout []*printform.LayoutForm
	for _, rec := range recs {
		if printform.IsLayoutV2(rec.Content) {
			lt, err := printform.ParseLayoutBytes(rec.Content)
			if err != nil {
				fmt.Printf("extform: пропускаю v2-форму %s/%s: %v\n", rec.Document, rec.Name, err)
				continue
			}
			name := lt.Name
			if name == "" {
				name = rec.Name
			}
			doc := lt.Document
			if doc == "" {
				doc = rec.Document
			}
			layout = append(layout, &printform.LayoutForm{Name: name, Document: doc, Layout: lt})
			continue
		}
		pf, err := printform.ParseBytes(rec.Content)
		if err != nil {
			fmt.Printf("extform: пропускаю форму %s/%s: %v\n", rec.Document, rec.Name, err)
			continue
		}
		if pf.Name == "" {
			pf.Name = rec.Name
		}
		if pf.Document == "" {
			pf.Document = rec.Document
		}
		legacy = append(legacy, pf)
	}
	return legacy, layout, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecord(rows rowScanner) (*Record, error) {
	var rec Record
	var enabled, author, version, uploadedBy, uploadedAt any
	if err := rows.Scan(&rec.ID, &rec.Document, &rec.Name, &rec.Content, &enabled, &author, &version, &uploadedBy, &uploadedAt); err != nil {
		return nil, err
	}
	rec.Enabled = scanBool(enabled)
	rec.Author = scanString(author)
	rec.Version = scanString(version)
	rec.UploadedBy = scanString(uploadedBy)
	rec.UploadedAt = storage.ParseDBTime(uploadedAt)
	return &rec, nil
}

func scanBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case int:
		return t != 0
	case []byte:
		return string(t) == "1" || strings.EqualFold(string(t), "true")
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	}
	return false
}

func scanString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	}
	return ""
}
