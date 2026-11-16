package resultfilter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/storage"
	"io"
)

const MaxSettingsBytes = 64 * 1024

// ParseSettings is strict at the report boundary: a broken payload must not
// silently turn a filtered report into an unfiltered one.
func ParseSettings(raw string) (*reportpkg.UserReportSettings, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > MaxSettingsBytes {
		return nil, fmt.Errorf("настройки отчёта слишком велики")
	}
	var settings reportpkg.UserReportSettings
	dec := json.NewDecoder(bytes.NewBufferString(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&settings); err != nil {
		return nil, fmt.Errorf("некорректные настройки отчёта")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("некорректные настройки отчёта")
	}
	if len(settings.Filters) > MaxConditions {
		return nil, fmt.Errorf("слишком много условий отбора")
	}
	return &settings, nil
}

// UserSettings records the owner-scoped variant used by both HTTP surfaces.
type UserSettings struct {
	Settings *reportpkg.UserReportSettings
	PresetID string
}

func ResolveSettings(ctx context.Context, db *storage.DB, report, user, raw, preset string, hasPreset bool) (UserSettings, error) {
	if raw != "" {
		st, err := ParseSettings(raw)
		return UserSettings{Settings: st, PresetID: preset}, err
	}
	if hasPreset && (preset == "" || preset == storage.StandardReportPresetID) {
		return UserSettings{PresetID: storage.StandardReportPresetID}, nil
	}
	if db == nil {
		if hasPreset {
			return UserSettings{}, fmt.Errorf("вариант отчёта недоступен")
		}
		return UserSettings{}, nil
	}
	if hasPreset {
		p, err := db.GetReportPreset(ctx, report, user, preset)
		if err != nil || p == nil {
			return UserSettings{}, fmt.Errorf("вариант отчёта недоступен")
		}
		st, err := ParseSettings(p.SettingsJSON)
		return UserSettings{Settings: st, PresetID: p.ID}, err
	}
	p, err := db.GetDefaultReportPreset(ctx, report, user)
	if err != nil {
		return UserSettings{}, err
	}
	if p != nil {
		st, err := ParseSettings(p.SettingsJSON)
		return UserSettings{Settings: st, PresetID: p.ID}, err
	}
	legacy, err := db.GetReportUserSettings(ctx, report, user)
	if err != nil {
		return UserSettings{}, err
	}
	st, err := ParseSettings(legacy)
	return UserSettings{Settings: st}, err
}
