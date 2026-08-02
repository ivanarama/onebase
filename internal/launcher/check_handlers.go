package launcher

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/ivantit66/onebase/internal/configdb"
)

// configuratorCheck is the per-fragment endpoint. It accepts ad-hoc text from
// the module/widget/home-page editors and validates it without touching the
// stored configuration. The "kind" field selects the validator.
func (h *handler) configuratorCheck(w http.ResponseWriter, r *http.Request) {
	if _, err := h.store.Get(chi.URLParam(r, "id")); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	lang := resolveLang(r)
	kind := r.FormValue("kind")
	source := r.FormValue("source")
	name := strings.TrimSpace(r.FormValue("name"))

	var issues []configcheck.Issue
	switch kind {
	case "dsl":
		issues = configcheck.CheckDSLSource(source, name)
	case "widget":
		issues = configcheck.CheckWidgetYAML(source, name)
	case "home_page":
		issues = configcheck.CheckHomePageYAML(source)
	case "entity":
		issues = configcheck.CheckEntityYAML(source, name)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": tr(lang, "неизвестный kind") + ": " + kind})
		return
	}
	writeJSON(w, http.StatusOK, configcheck.NewResult(issues))
}

// configuratorCheckAll walks the whole configuration and reports every
// detectable error: YAML parse failures, DSL syntax errors, and compile errors
// for queries declared in widgets/reports.
func (h *handler) configuratorCheckAll(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)

	dir, cleanup, err := materializeProject(r.Context(), h, b)
	if err != nil {
		writeJSON(w, http.StatusOK, configcheck.Result{
			OK:     false,
			Total:  1,
			Issues: []configcheck.Issue{{Message: tr(lang, "не удалось получить конфигурацию") + ": " + err.Error()}},
		})
		return
	}
	if cleanup != nil {
		defer cleanup()
	}

	writeJSON(w, http.StatusOK, configcheck.RunFull(dir))
}

// materializeProject ensures the configuration is available as a directory tree
// so configcheck.CheckDir + project.Load can run. For file-backed bases that's
// just b.Path; for db-backed bases we export into a temp dir and the cleanup
// callback removes it on the way out.
func materializeProject(ctx context.Context, h *handler, b *Base) (dir string, cleanup func(), err error) {
	if b.ConfigSource != "database" {
		return b.Path, nil, nil
	}
	db, dberr := OpenDB(ctx, b)
	if dberr != nil {
		return "", nil, dberr
	}
	tmp, terr := os.MkdirTemp("", "onebase-check-")
	if terr != nil {
		db.Close()
		return "", nil, terr
	}
	repo := configdb.New(db)
	if exporr := repo.ExportToDir(ctx, tmp); exporr != nil {
		db.Close()
		removeTemp(tmp)
		return "", nil, exporr
	}
	return tmp, func() { db.Close(); removeTemp(tmp) }, nil
}
