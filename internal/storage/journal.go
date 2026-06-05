package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// colExprForDoc returns the SQL expression for a journal column in the given entity's SELECT.
// Returns ("NULL", nil) if the column has no matching field in the entity.
//
// приоритет резолва:
//   1. jcol.Map[entity.Name] — явный per-doc mapping (новый рекомендуемый путь).
//   2. Exact match — у документа есть поле с именем jcol.Field.
//   3. Fallback list — старый fallback (back compat); COALESCE если несколько.
func colExprForDoc(jcol metadata.JournalColumn, entity *metadata.Entity) (string, *metadata.Field) {
	// 1. Explicit map для этого документа.
	if jcol.Map != nil {
		if mapped, ok := jcol.Map[entity.Name]; ok && mapped != "" {
			for i := range entity.Fields {
				f := &entity.Fields[i]
				if strings.EqualFold(f.Name, mapped) {
					return metadata.ColumnName(*f), f
				}
			}
			// Map ссылается на несуществующее поле — намеренно
			// возвращаем NULL, не падая на fallback'е молча.
			return "NULL", nil
		}
	}
	// 2. Exact match
	for i := range entity.Fields {
		f := &entity.Fields[i]
		if strings.EqualFold(f.Name, jcol.Field) {
			return metadata.ColumnName(*f), f
		}
	}
	// 3. Fallback match — build COALESCE of all matching columns
	type match struct {
		col string
		f   *metadata.Field
	}
	var found []match
	for _, fb := range jcol.Fallback {
		for i := range entity.Fields {
			f := &entity.Fields[i]
			if strings.EqualFold(f.Name, fb) {
				found = append(found, match{metadata.ColumnName(*f), f})
				break
			}
		}
	}
	if len(found) == 0 {
		return "NULL", nil
	}
	if len(found) == 1 {
		return found[0].col, found[0].f
	}
	cols := make([]string, len(found))
	for i, m := range found {
		cols[i] = m.col
	}
	return "COALESCE(" + strings.Join(cols, ", ") + ")", found[0].f
}

// ColRefMap maps journal column names (lowercase) to the ref entity name (if the column is a reference).
type ColRefMap map[string]string

// JournalQuery executes a UNION ALL across all documents in the journal.
// Returns rows, total count, and a ColRefMap for ref resolution.
func (db *DB) JournalQuery(
	ctx context.Context,
	j *metadata.Journal,
	docs map[string]*metadata.Entity,
	params ListParams,
	limit, offset int,
) ([]map[string]any, int, ColRefMap, error) {
	if limit <= 0 {
		limit = 100
	}

	colRefMap := make(ColRefMap)

	// Build the column alias list (output names for the CTE)
	outCols := make([]string, len(j.Columns))
	for i, jcol := range j.Columns {
		outCols[i] = strings.ToLower(jcol.Field)
	}

	// Build one SELECT per document
	var selects []string
	for _, docName := range j.Documents {
		entity, ok := docs[docName]
		if !ok {
			continue
		}
		table := metadata.TableName(docName)

		parts := []string{
			fmt.Sprintf("'%s' AS _doc_kind", docName),
			"id",
		}
		for i, jcol := range j.Columns {
			expr, f := colExprForDoc(jcol, entity)
			alias := outCols[i]
			parts = append(parts, fmt.Sprintf("%s AS %s", expr, alias))
			// Populate colRefMap from first doc that has a ref field for this column
			if f != nil && f.RefEntity != "" {
				if _, exists := colRefMap[alias]; !exists {
					colRefMap[alias] = f.RefEntity
				}
			}
		}
		selects = append(selects, fmt.Sprintf("SELECT %s FROM %s", strings.Join(parts, ", "), table))
	}

	if len(selects) == 0 {
		return nil, 0, colRefMap, nil
	}

	union := strings.Join(selects, "\nUNION ALL\n")

	// Build WHERE from filters
	var whereParts []string
	var args []any
	argIdx := 1

	d := db.dialect
	for _, jf := range j.Filters {
		fv, ok := params.Filters[jf.Field]
		if !ok {
			continue
		}
		colName := strings.ToLower(jf.Field)
		switch {
		case jf.Type == "date_range":
			if fv.From != "" {
				whereParts = append(whereParts, fmt.Sprintf("%s >= %s", colName, d.Placeholder(argIdx)))
				args = append(args, fv.From)
				argIdx++
			}
			if fv.To != "" {
				whereParts = append(whereParts, fmt.Sprintf("%s <= %s", colName, d.Placeholder(argIdx)))
				args = append(args, fv.To)
				argIdx++
			}
		case strings.HasPrefix(jf.Type, "reference:"):
			if fv.Value != "" {
				whereParts = append(whereParts, fmt.Sprintf("%s = %s", colName, d.Placeholder(argIdx)))
				if id, err := uuid.Parse(fv.Value); err == nil {
					args = append(args, idArg(d, id))
				} else {
					args = append(args, fv.Value)
				}
				argIdx++
			}
		default:
			if fv.Value != "" {
				whereParts = append(whereParts, d.LowerLike(colName)+" LIKE "+d.LowerLike(d.Placeholder(argIdx)))
				args = append(args, "%"+fv.Value+"%")
				argIdx++
			}
		}
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = " WHERE " + strings.Join(whereParts, " AND ")
	}

	// Determine ORDER BY: use first date column (by name heuristic) or id
	orderCol := "id"
	for _, jcol := range j.Columns {
		lf := strings.ToLower(jcol.Field)
		if lf == "дата" || lf == "date" || strings.Contains(lf, "дата") {
			orderCol = lf
			break
		}
	}

	// Count query
	countSQL := fmt.Sprintf("WITH j AS (%s)\nSELECT COUNT(*) FROM j%s", union, whereClause)
	var total int
	if err := db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, colRefMap, fmt.Errorf("journal count: %w", err)
	}

	// Data query. NULLS LAST is PG-specific; on SQLite ORDER BY DESC already
	// puts NULLs at the bottom, so we emit it only on PG.
	nullsClause := ""
	if d.Name() == "postgres" {
		nullsClause = " NULLS LAST"
	}
	dataSQL := fmt.Sprintf(
		"WITH j AS (%s)\nSELECT _doc_kind, id, %s FROM j%s ORDER BY %s DESC%s LIMIT %s OFFSET %s",
		union,
		strings.Join(outCols, ", "),
		whereClause,
		orderCol,
		nullsClause,
		d.Placeholder(argIdx), d.Placeholder(argIdx+1),
	)
	dataArgs := append(args, limit, offset)

	rows2, err := db.Query(ctx, dataSQL, dataArgs...)
	if err != nil {
		return nil, 0, colRefMap, fmt.Errorf("journal query: %w", err)
	}
	defer rows2.Close()

	// Scan results: [_doc_kind, id, col0, col1, ...]
	var rows []map[string]any
	for rows2.Next() {
		n := len(j.Columns) + 2 // _doc_kind + id + columns
		dest := make([]any, n)
		ptrs := make([]any, n)
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows2.Scan(ptrs...); err != nil {
			return nil, 0, colRefMap, fmt.Errorf("journal scan: %w", err)
		}
		row := make(map[string]any, n)
		row["_doc_kind"] = normalizeValue(dest[0])
		row["id"] = normalizeValue(dest[1])
		for i, jcol := range j.Columns {
			row[jcol.Field] = normalizeValue(dest[i+2])
		}
		rows = append(rows, row)
	}
	return rows, total, colRefMap, rows2.Err()
}
