package ui

import (
	"context"
	"fmt"
	"html/template"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/widget"
)

// Служебные ключи строки списка с оформлением row_style. NUL в начале не
// пересекается ни с реквизитом конфигурации, ни с колонкой SQL.
const (
	listRowBackgroundKey = "\x00list_row_bg"
	listRowBoldKey       = "\x00list_row_bold"
	listRowColorKey      = "\x00list_row_fg"
)

// applyListRowStyle — оформление строк формы списка (row_style формы kind:
// list), как ПриВыводеСтроки динамического списка 1С. Фон — первое совпавшее
// правило, иначе цвет из пути background_from; жирный — совпавшее правило с
// bold или признак из пути bold_from.
//
// Вызывается после маскирования и до разрешения ссылок: пути идут по сырым
// идентификаторам, а замаскированное поле стиль не даёт и значение не выдаёт.
// Связанная запись читается только при праве чтения и строковом доступе и
// тоже маскируется; недоступная — просто не даёт оформления.
func (s *Server) applyListRowStyle(ctx context.Context, entity *metadata.Entity, rows []map[string]any) {
	form := pickManagedFormCtx(ctx, entity, "list")
	if form == nil || form.ListRowStyle == nil || len(rows) == 0 {
		return
	}
	style := form.ListRowStyle
	cache := map[string]map[string]any{}
	for _, row := range rows {
		bg, bold, color := "", false, ""
		for _, rule := range style.Rules {
			v := s.listRowPathValue(ctx, entity, row, rule.Field, cache)
			equals := rule.Equals
			if equals == metadata.ListFilterCurrentUser {
				u := auth.UserFromContext(ctx)
				if u == nil || u.ID == "" {
					continue
				}
				equals = u.ID
			}
			if v == nil || strings.TrimSpace(fmt.Sprint(v)) != strings.TrimSpace(equals) {
				continue
			}
			if c := widget.NormalizeRowColor(rule.Background); c != "" && bg == "" {
				bg = c
			}
			bold = bold || rule.Bold
			if c := widget.NormalizeRowColor(rule.Color); c != "" && color == "" {
				color = c
			}
		}
		if bg == "" && style.BackgroundFrom != "" {
			bg = widget.NormalizeRowColor(s.listRowPathValue(ctx, entity, row, style.BackgroundFrom, cache))
		}
		if !bold && style.BoldFrom != "" {
			bold = listRowTruthy(s.listRowPathValue(ctx, entity, row, style.BoldFrom, cache))
		}
		if color == "" && style.ColorFrom != "" {
			color = widget.NormalizeRowColor(s.listRowPathValue(ctx, entity, row, style.ColorFrom, cache))
		}
		if color != "" {
			row[listRowColorKey] = color
		}
		if bg != "" {
			row[listRowBackgroundKey] = bg
		}
		if bold {
			row[listRowBoldKey] = true
		}
	}
}

// listRowPathValue — значение пути «Реквизит» или «Ссылка.Реквизит…» строки.
func (s *Server) listRowPathValue(ctx context.Context, entity *metadata.Entity, row map[string]any, path string, cache map[string]map[string]any) any {
	parts := strings.Split(strings.TrimSpace(path), ".")
	curEntity, curRow := entity, row
	for i, part := range parts {
		key, ok := rowKeyByName(curRow, part)
		if !ok {
			return nil
		}
		v := curRow[key]
		if i == len(parts)-1 {
			return v
		}
		field, ok := entityFieldByName(curEntity, part)
		if !ok || field.RefEntity == "" {
			return nil
		}
		target := s.reg.GetEntity(field.RefEntity)
		id := refValueString(v)
		if target == nil || id == "" {
			return nil
		}
		cacheKey := target.Name + "|" + id
		rec, seen := cache[cacheKey]
		if !seen {
			rec = s.listRowStyleRef(ctx, target, id)
			cache[cacheKey] = rec
		}
		if rec == nil {
			return nil
		}
		curEntity, curRow = target, rec
	}
	return nil
}

// listRowStyleRef читает связанную запись для пути оформления: право чтения,
// строковый доступ и маска полей — как у любого чтения интерфейса.
func (s *Server) listRowStyleRef(ctx context.Context, entity *metadata.Entity, rawID string) map[string]any {
	if u := auth.UserFromContext(ctx); u != nil && !u.Has(string(entity.Kind), entity.Name, "read") {
		return nil
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return nil
	}
	rec, err := s.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil || rec == nil || !s.rowAllowedContext(ctx, entity, "read", rec) {
		return nil
	}
	s.maskRecord(ctx, entity, rec)
	return rec
}

func listRowTruthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int, int32, int64, float32, float64:
		return fmt.Sprint(x) != "0"
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "истина", "да":
			return true
		}
	}
	return false
}

// listRowStyle — inline-стиль строки списка из row_style. Значения повторно
// проверяются: в template.CSS попадают только цвет "#rrggbb" и font-weight.
func listRowStyle(row map[string]any) template.CSS {
	var parts []string
	if bg := widget.NormalizeRowColor(row[listRowBackgroundKey]); bg != "" {
		parts = append(parts, "background-color:"+bg, "color:"+widget.RowTextColor(bg))
	}
	if bold, _ := row[listRowBoldKey].(bool); bold {
		parts = append(parts, "font-weight:600")
	}
	if color := widget.NormalizeRowColor(row[listRowColorKey]); color != "" {
		parts = append(parts, "color:"+color)
	}
	return template.CSS(strings.Join(parts, ";")) //nolint:gosec // G203: только проверенный цвет #rrggbb и font-weight
}
