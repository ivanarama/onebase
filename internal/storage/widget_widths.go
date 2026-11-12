package storage

import (
	"context"
	"encoding/json"
	"fmt"
)

// Widget widths use stable auth IDs, not logins: rename does not move this key.
func widgetWidthsKey(widget, userID string) string {
	return fmt.Sprintf("widget.widths.id.%d:%s.%d:%s", len(widget), widget, len(userID), userID)
}

func (db *DB) GetWidgetColumnWidths(ctx context.Context, widget, userID string) map[string]int {
	if db.EnsureSettingsSchema(ctx) != nil {
		return nil
	}
	var raw string
	if db.QueryRow(ctx, "SELECT value FROM _settings WHERE key = "+db.dialect.Placeholder(1), widgetWidthsKey(widget, userID)).Scan(&raw) != nil {
		return nil
	}
	var widths map[string]int
	if json.Unmarshal([]byte(raw), &widths) != nil {
		return nil
	}
	return widths
}

func (db *DB) SaveWidgetColumnWidths(ctx context.Context, widget, userID string, widths map[string]int) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	key := widgetWidthsKey(widget, userID)
	if len(widths) == 0 {
		_, err := db.Exec(ctx, "DELETE FROM _settings WHERE key = "+d.Placeholder(1), key)
		return err
	}
	raw, err := json.Marshal(widths)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, fmt.Sprintf("INSERT INTO _settings (key,value) VALUES (%s,%s) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value", d.Placeholder(1), d.Placeholder(2)), key, string(raw))
	return err
}
