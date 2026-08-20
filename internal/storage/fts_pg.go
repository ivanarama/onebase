package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// pgFullText — реализация полнотекстового поиска на PostgreSQL: колонка
// search_tsv (материализованная, GENERATED ALWAYS) и GIN-индекс по ней.
// Генерируемая колонка выбрана вместо триггера сознательно: она не может
// разъехаться с title/body, потому что пересчитывается самой СУБД.
type pgFullText struct{}

// pgFTSConfig — конфигурация текстового поиска. russian даёт морфологию
// («договоры» находится по «договор»); в сборках без русского словаря
// откатываемся на simple — поиск станет по словоформам, но не сломается.
const (
	pgFTSConfigRussian = "russian"
	pgFTSConfigSimple  = "simple"
)

// ftsConfig возвращает имя конфигурации текстового поиска, кэшируя ответ на
// процесс: он не меняется в течение жизни базы, а спрашивать при каждом
// поиске — лишний round-trip.
func (db *DB) ftsConfig(ctx context.Context) string {
	if v := db.ftsCfg.Load(); v != nil {
		return v.(string)
	}
	cfg := pgFTSConfigSimple
	var exists bool
	if err := db.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_ts_config WHERE cfgname = $1)", pgFTSConfigRussian,
	).Scan(&exists); err != nil {
		// Проба не удалась — не кэшируем, чтобы следующий вызов попробовал снова.
		return pgFTSConfigSimple
	}
	if exists {
		cfg = pgFTSConfigRussian
	}
	db.ftsCfg.Store(cfg)
	return cfg
}

func (pgFullText) EnsureSchema(ctx context.Context, db *DB) error {
	cfg := db.ftsConfig(ctx)
	// Вес A у представления, B у остального текста: совпадение в наименовании
	// должно обгонять совпадение в комментарии.
	typ := fmt.Sprintf(
		`tsvector GENERATED ALWAYS AS (setweight(to_tsvector('%s'::regconfig, coalesce(title,'')), 'A') || setweight(to_tsvector('%s'::regconfig, coalesce(body,'')), 'B')) STORED`,
		cfg, cfg)
	if err := db.AddColumnIfMissing(ctx, ftsTable, "search_tsv", typ); err != nil {
		return fmt.Errorf("полнотекстовый индекс: search_tsv: %w", err)
	}
	if _, err := db.Exec(ctx,
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_fts_tsv ON %s USING GIN (search_tsv)", ftsTable),
	); err != nil {
		return fmt.Errorf("полнотекстовый индекс: GIN: %w", err)
	}
	return nil
}

func (pgFullText) Search(ctx context.Context, db *DB, q FTSQuery) ([]FTSHit, error) {
	tsq := pgTSQuery(ftsTokens(q.Text))
	if tsq == "" {
		return nil, nil
	}
	args := []any{db.ftsConfig(ctx), tsq}
	where := "f.search_tsv @@ q"
	if len(q.Names) > 0 {
		args = append(args, q.Names)
		where += fmt.Sprintf(" AND f.owner_name = ANY($%d)", len(args))
	}
	scopeSQL, scopeArgs, _, err := ftsScopeSQL(db.dialect, q.Scopes, len(args)+1)
	if err != nil {
		return nil, err
	}
	if scopeSQL != "" {
		where += " AND (" + scopeSQL + ")"
		args = append(args, scopeArgs...)
	}
	args = append(args, q.Limit, q.Offset)
	sqlText := fmt.Sprintf(`
		SELECT f.owner_kind, f.owner_name, f.owner_id, f.title, ts_rank(f.search_tsv, q) AS rank
		FROM %s f, to_tsquery($1::regconfig, $2) q
		WHERE %s
		ORDER BY rank DESC, f.owner_name, f.owner_id
		LIMIT $%d OFFSET $%d`, ftsTable, where, len(args)-1, len(args))
	return scanFTSHits(ctx, db, sqlText, args...)
}

// pgTSQuery собирает tsquery из токенов: каждое слово ищется как префикс
// («рома» находит «Ромашку»), слова соединяются по И. Токены уже очищены до
// букв и цифр, поэтому служебные символы tsquery в выражение не попадают.
func pgTSQuery(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		parts = append(parts, t+":*")
	}
	return strings.Join(parts, " & ")
}

func scanFTSHits(ctx context.Context, db *DB, sqlText string, args ...any) ([]FTSHit, error) {
	rows, err := db.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("полнотекстовый поиск: %w", err)
	}
	defer rows.Close()
	var out []FTSHit
	for rows.Next() {
		var (
			hit   FTSHit
			idStr string
		)
		if err := rows.Scan(&hit.Kind, &hit.Name, &idStr, &hit.Title, &hit.Rank); err != nil {
			return nil, fmt.Errorf("полнотекстовый поиск: %w", err)
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("полнотекстовый поиск: owner_id: %w", err)
		}
		hit.ID = id
		out = append(out, hit)
	}
	return out, rows.Err()
}
