package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

const maxJournalSettingsBytes = 32 * 1024

var errJournalSettingsTooLarge = errors.New("настройки журнала слишком велики")

type JournalUserSettings struct {
	Columns []JournalColumnSetting `json:"columns,omitempty"`
}

type JournalColumnSetting struct {
	Field   string `json:"field"`
	Visible bool   `json:"visible"`
}

type journalColumnUI struct {
	Column  metadata.JournalColumn
	Visible bool
}

func parseJournalSettings(raw string) (*JournalUserSettings, error) {
	if raw == "" {
		return nil, nil
	}
	var s JournalUserSettings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *JournalUserSettings) JSON() (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func loadJournalSettings(store journalSettingsStore, r *http.Request, j *metadata.Journal) *JournalUserSettings {
	if store == nil || j == nil {
		return nil
	}
	raw, err := store.GetJournalUserSettings(r.Context(), j.Name, currentUserLogin(r))
	if err != nil || raw == "" {
		return nil
	}
	st, err := parseJournalSettings(raw)
	if err != nil {
		return nil
	}
	return st
}

type journalSettingsStore interface {
	GetJournalUserSettings(ctx context.Context, journal, user string) (string, error)
}

func effectiveJournalColumns(j *metadata.Journal, st *JournalUserSettings) []metadata.JournalColumn {
	ui := journalSettingsColumns(j, st)
	out := make([]metadata.JournalColumn, 0, len(ui))
	for _, c := range ui {
		if c.Visible {
			out = append(out, c.Column)
		}
	}
	return out
}

func journalSettingsColumns(j *metadata.Journal, st *JournalUserSettings) []journalColumnUI {
	if j == nil {
		return nil
	}
	byField := make(map[string]metadata.JournalColumn, len(j.Columns))
	for _, c := range j.Columns {
		byField[strings.ToLower(c.Field)] = c
	}
	seen := make(map[string]bool, len(j.Columns))
	out := make([]journalColumnUI, 0, len(j.Columns))
	if st != nil {
		for _, pref := range st.Columns {
			key := strings.ToLower(pref.Field)
			col, ok := byField[key]
			if !ok || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, journalColumnUI{Column: col, Visible: pref.Visible})
		}
	}
	for _, col := range j.Columns {
		key := strings.ToLower(col.Field)
		if seen[key] {
			continue
		}
		out = append(out, journalColumnUI{Column: col, Visible: true})
	}
	return out
}

func canonicalJournalSettings(j *metadata.Journal, st *JournalUserSettings) *JournalUserSettings {
	if j == nil {
		return nil
	}
	cols := journalSettingsColumns(j, st)
	out := &JournalUserSettings{Columns: make([]JournalColumnSetting, 0, len(cols))}
	for _, c := range cols {
		out.Columns = append(out.Columns, JournalColumnSetting{
			Field:   c.Column.Field,
			Visible: c.Visible,
		})
	}
	return out
}

func journalSettingsJSON(j *metadata.Journal, st *JournalUserSettings) string {
	canon := canonicalJournalSettings(j, st)
	if canon == nil {
		return ""
	}
	raw, err := canon.JSON()
	if err != nil {
		return ""
	}
	return raw
}

func journalReturnURL(r *http.Request, fallback string) string {
	raw := strings.TrimSpace(r.FormValue("__return"))
	if raw == "" {
		return fallback
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || !strings.HasPrefix(u.Path, "/ui/journal/") {
		return fallback
	}
	return u.RequestURI()
}

func (s *Server) journalSettingsSave(w http.ResponseWriter, r *http.Request) {
	j := s.getJournal(w, r)
	if j == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, s.errText(r, err), http.StatusBadRequest)
		return
	}
	raw := r.FormValue("__journal_settings")
	if len(raw) > maxJournalSettingsBytes {
		http.Error(w, s.errText(r, errJournalSettingsTooLarge), http.StatusRequestEntityTooLarge)
		return
	}
	st, err := parseJournalSettings(raw)
	if err != nil {
		http.Error(w, s.errText(r, err), http.StatusBadRequest)
		return
	}
	canon := journalSettingsJSON(j, st)
	if len(canon) > maxJournalSettingsBytes {
		http.Error(w, s.errText(r, errJournalSettingsTooLarge), http.StatusRequestEntityTooLarge)
		return
	}
	_ = s.store.SaveJournalUserSettings(r.Context(), j.Name, currentUserLogin(r), canon)
	http.Redirect(w, r, journalReturnURL(r, journalFormURL(j.Name)), http.StatusSeeOther)
}

func (s *Server) journalSettingsReset(w http.ResponseWriter, r *http.Request) {
	j := s.getJournal(w, r)
	if j == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, s.errText(r, err), http.StatusBadRequest)
		return
	}
	_ = s.store.DeleteJournalUserSettings(r.Context(), j.Name, currentUserLogin(r))
	http.Redirect(w, r, journalReturnURL(r, journalFormURL(j.Name)), http.StatusSeeOther)
}

func journalFormURL(name string) string {
	return "/ui/journal/" + url.PathEscape(strings.ToLower(name))
}
