package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// IDsInSubtree — какие из ids лежат в поддереве группы root иерархического
// справочника: сама группа и всё, что вложено в неё на любую глубину. Нужен
// оформлению строк выбора («элемент в группе Архивные» — choice_row_style
// when_in_folder): одна выборка на страницу вариантов, без обхода по строке.
func (db *DB) IDsInSubtree(ctx context.Context, entity *metadata.Entity, root uuid.UUID, ids []uuid.UUID) (map[string]bool, error) {
	out := make(map[string]bool)
	if entity == nil || len(ids) == 0 || root == uuid.Nil {
		return out, nil
	}
	d := db.dialect
	table := metadata.TableName(entity.Name)
	// Аргументы — в порядке появления в тексте: у SQLite плейсхолдеры позиционные.
	args := make([]any, 0, len(ids)+1)
	marks := make([]string, 0, len(ids))
	for _, id := range ids {
		args = append(args, idArg(d, id))
		marks = append(marks, d.Placeholder(len(args)))
	}
	args = append(args, idArg(d, root))
	query := "SELECT id FROM " + table + " WHERE id IN (" + strings.Join(marks, ", ") + ") AND id IN " + choiceSubtreeSQL(entity.Name, d.Placeholder(len(args)))
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out[strings.ToLower(fmt.Sprint(normalizeValue(raw)))] = true
	}
	return out, rows.Err()
}

// FolderIDsByName — группы иерархического справочника с точно таким
// наименованием. Для when_in_folder, когда группа не предопределённая (пришла
// из 1С данными): без строкового реквизита «Наименование» — пусто.
func (db *DB) FolderIDsByName(ctx context.Context, entity *metadata.Entity, name string) ([]uuid.UUID, error) {
	if entity == nil || !entity.Hierarchical || strings.TrimSpace(name) == "" {
		return nil, nil
	}
	var column string
	for _, f := range entity.Fields {
		if f.Type == metadata.FieldTypeString && strings.EqualFold(f.Name, "Наименование") {
			column = metadata.ColumnName(f)
		}
	}
	if column == "" {
		return nil, nil
	}
	d := db.dialect
	query := "SELECT id FROM " + metadata.TableName(entity.Name) + " WHERE is_folder = " + boolTrueLit(d) + " AND " + column + " = " + d.Placeholder(1)
	rows, err := db.Query(ctx, query, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if id, err := uuid.Parse(fmt.Sprint(normalizeValue(raw))); err == nil {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}
