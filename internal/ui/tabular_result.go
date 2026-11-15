package ui

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/richtext"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/sheet"
	"github.com/shopspring/decimal"
)

type tabularScope struct {
	Kind, Name, ID, Form, Field string
	CustomDrilldown             bool
}
type tabularResult struct {
	doc        *sheet.Document
	owner      [32]byte
	scope      tabularScope
	expires    time.Time
	generation uint64
}
type tabularResultStore struct {
	mu    sync.Mutex
	items map[string]*tabularResult
}

func tabularOwner(r *http.Request) ([32]byte, error) {
	session := ""
	if c, e := r.Cookie("onebase_session"); e == nil {
		session = c.Value
	}
	if session == "" {
		if c, e := r.Cookie("onebase_tabular_view"); e == nil {
			session = c.Value
		}
	}
	if session == "" {
		return [32]byte{}, fmt.Errorf("session required")
	}
	// Includes current roles, masks, RLS attributes, admin and actor identity.
	data, err := json.Marshal(struct {
		Session string
		User    *auth.User
	}{session, auth.UserFromContext(r.Context())})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(data), nil
}

func (s *Server) publishTabular(w http.ResponseWriter, r *http.Request, scope tabularScope, doc *sheet.Document) (string, error) {
	if doc == nil {
		return "", nil
	}
	if _, err := tabularOwner(r); err != nil {
		// Anonymous local deployments still bind results to a private browser session.
		if auth.UserFromContext(r.Context()) != nil {
			return "", err
		}
		c := &http.Cookie{Name: "onebase_tabular_view", Value: uuid.NewString(), Path: "/ui", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode}
		http.SetCookie(w, c)
		r.AddCookie(c)
	}
	owner, err := tabularOwner(r)
	if err != nil {
		return "", err
	}
	maxRow, maxCol := doc.ContentBounds()
	// A 67-column KPI with 830 rows uses 53,893 styled cells. Keep the
	// occupied-cell budget within the existing 100,000-position rectangle;
	// byte/payload/row/column budgets below remain independent and unchanged.
	if maxRow > 20000 || maxCol > 512 || len(doc.Cells) > 100000 || (maxRow+1)*(maxCol+1) > 100000 {
		return "", fmt.Errorf("табличный документ превышает предел просмотра")
	}
	bytes := 0
	for _, c := range doc.Cells {
		if c != nil {
			bytes += len(c.Text) + len(c.RichHTML) + len(c.Picture)
		}
	}
	if a := doc.HeaderArea; a != nil {
		if a.Top < 0 || a.Left < 0 || a.Rows() < 1 || a.Rows() > 20000 || a.Cols() < 1 || a.Cols() > 512 || a.Rows()*a.Cols() > 100000 {
			return "", fmt.Errorf("некорректная повторяемая область")
		}
		for _, c := range a.Cells {
			if c != nil {
				bytes += len(c.Text) + len(c.RichHTML) + len(c.Picture)
			}
		}
	}
	if bytes > 8<<20 {
		return "", fmt.Errorf("табличный документ превышает предел просмотра")
	}
	frozen := doc.Snapshot()
	payloadBytes, err := interpreter.FreezeTabularDrilldowns(frozen)
	if err != nil {
		return "", err
	}
	if bytes+payloadBytes > 8<<20 {
		return "", fmt.Errorf("табличный документ превышает предел просмотра")
	}
	for key, c := range frozen.Cells {
		if key.Row < 0 || key.Col < 0 || c.ColSpan > 512 || c.RowSpan > 20000 {
			return "", fmt.Errorf("некорректная область табличного документа")
		}
		sanitizeTabularCell(c)
		// Freeze builder objects too: only immutable scalar values enter serializers.
		switch c.Value.(type) {
		case nil, string, bool, float64, float32, int, int64, int32, uint, uint64, time.Time, decimal.Decimal:
		default:
			c.Value = c.Text
		}
	}
	if frozen.HeaderArea != nil {
		for _, c := range frozen.HeaderArea.Cells {
			if c != nil {
				sanitizeTabularCell(c)
			}
		}
	}
	store := &s.tabularResults
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.items == nil {
		store.items = make(map[string]*tabularResult)
	}
	now := time.Now()
	oldest := ""
	for k, v := range store.items {
		if now.After(v.expires) {
			delete(store.items, k)
			continue
		}
		if oldest == "" || v.expires.Before(store.items[oldest].expires) {
			oldest = k
		}
	}
	if len(store.items) >= 16 && oldest != "" {
		delete(store.items, oldest)
	}
	token := uuid.NewString()
	store.items[token] = &tabularResult{doc: frozen, scope: scope, owner: owner, expires: now.Add(15 * time.Minute), generation: s.reg.Generation()}
	return "/ui/tabular/" + token, nil
}

func (s *Server) readableTabular(r *http.Request, token string) (*tabularResult, bool) {
	owner, err := tabularOwner(r)
	if err != nil {
		return nil, false
	}
	store := &s.tabularResults
	store.mu.Lock()
	v := store.items[token]
	store.mu.Unlock()
	if v == nil || time.Now().After(v.expires) || v.owner != owner || v.generation != s.reg.Generation() {
		return nil, false
	}
	scope := v.scope
	switch scope.Kind {
	case "report":
		if s.reg.GetReport(scope.Name) == nil || !s.can(r, "report", scope.Name, "run") {
			return nil, false
		}
	case "processor":
		p := s.reg.GetProcessor(scope.Name)
		if p == nil || !s.can(r, "processor", scope.Name, "run") || !s.canRunExternalProc(r, p) {
			return nil, false
		}
	default:
		e := s.reg.GetEntity(scope.Name)
		if e == nil || !s.can(r, string(e.Kind), e.Name, "read") {
			return nil, false
		}
		if scope.ID != "" {
			id, err := uuid.Parse(scope.ID)
			if err != nil || !s.rowAllowsID(r.Context(), e, "read", id) {
				return nil, false
			}
		}
	}
	return v, true
}

func (s *Server) tabularView(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	v, ok := s.readableTabular(r, token)
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	base := "/ui/tabular/" + url.PathEscape(token)
	switch chi.URLParam(r, "format") {
	case "xlsx", "pdf":
		_, finish, allowed := s.beginOperation(r, opReportExport, v.scope.Name)
		if !allowed {
			http.Error(w, "export limit", http.StatusTooManyRequests)
			return
		}
		defer finish("ok", 0, false)
		var data []byte
		var err error
		ext := chi.URLParam(r, "format")
		if ext == "xlsx" {
			data, err = v.doc.XLSX()
			w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		} else {
			data, err = v.doc.PDF(sheet.PDFOptions{})
			w.Header().Set("Content-Type", "application/pdf")
		}
		if err != nil {
			http.Error(w, "export failed", 500)
			return
		}
		name := v.doc.FileName
		if name == "" {
			name = v.scope.Name
		}
		name = strings.Map(func(c rune) rune {
			if c < ' ' || strings.ContainsRune("/\\\";", c) {
				return '_'
			}
			return c
		}, name)
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(strings.TrimSuffix(name, "."+ext)+"."+ext))
		_, _ = w.Write(data)
	case "cell":
		row, e1 := strconv.Atoi(r.URL.Query().Get("row"))
		col, e2 := strconv.Atoi(r.URL.Query().Get("col"))
		if e1 != nil || e2 != nil {
			http.NotFound(w, r)
			return
		}
		c := v.doc.GetCell(row, col)
		if c == nil || c.Drilldown == nil {
			http.NotFound(w, r)
			return
		}
		target := s.reg.GetEntity(c.Drilldown.Entity)
		id, err := uuid.Parse(c.Drilldown.ID)
		if target == nil || err != nil || !s.can(r, string(target.Kind), target.Name, "read") || !s.rowAllowsID(r.Context(), target, "read", id) {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/ui/"+string(target.Kind)+"/"+url.PathEscape(target.Name)+"/"+id.String(), http.StatusSeeOther)
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if chi.URLParam(r, "format") != "" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(v.doc.HTML(sheet.HTMLOptions{PDFURL: base + "/pdf", XLSXURL: base + "/xlsx", Embedded: v.scope.Form != "", ParentDrilldown: v.scope.CustomDrilldown, DrilldownURL: func(row, col int) string {
			return base + "/cell?row=" + strconv.Itoa(row) + "&col=" + strconv.Itoa(col)
		}})))
	}
}

func sanitizeTabularCell(c *sheet.Cell) {
	c.RichHTML = richtext.Sanitize(c.RichHTML)
	// A result never fetches arbitrary remote images during viewing or export.
	if !strings.HasPrefix(c.Picture, "data:image/") {
		c.Picture = ""
	}
	switch c.Align {
	case "", "left", "center", "right", "justify":
	default:
		c.Align = "left"
	}
	switch c.Vertical {
	case "", "top", "center", "middle", "bottom":
	default:
		c.Vertical = "top"
	}
}

type tabularLayouts struct {
	s     *Server
	owner string
}

func (t *tabularLayouts) CallMethod(name string, args []any) any {
	if !strings.EqualFold(name, "Получить") && !strings.EqualFold(name, "Get") {
		return nil
	}
	if len(args) != 1 {
		interpreter.RaiseUserError("ПолучитьМакет: требуется имя")
	}
	wanted := fmt.Sprint(args[0])
	for _, layout := range t.s.reg.GetLayoutForms(t.owner) {
		if strings.EqualFold(layout.Name, wanted) {
			return interpreter.NewMaket(layout.Layout)
		}
	}
	interpreter.RaiseUserError("макет не найден")
	return nil
}

func (s *Server) attachTabularResults(w http.ResponseWriter, r *http.Request, scope tabularScope, f *formObjectThis, resp *formEventResponse) {
	if len(f.tabular) == 0 {
		return
	}
	resp.TabularDocuments = make(map[string]string, len(f.tabular))
	for name, doc := range f.tabular {
		fieldScope := scope
		if scope.Kind != "processor" && f.obj != nil && f.obj.ID != uuid.Nil {
			fieldScope.ID = f.obj.ID.String()
		}
		fieldScope.Field = name
		f.form.Walk(func(el *metadata.FormElement) bool {
			if el != nil && el.Kind == metadata.FormElementSpreadsheet && strings.EqualFold(formDataPathFieldName(el.DataPath), name) && el.Handlers[metadata.FormEventDrilldown] != "" {
				fieldScope.CustomDrilldown = true
			}
			return true
		})
		var model *sheet.Document
		if doc != nil {
			model = doc.Doc
		}
		path, err := s.publishTabular(w, r, fieldScope, model)
		if err != nil {
			resp.OK = false
			resp.Error = "не удалось открыть табличный документ"
			resp.TabularDocuments = nil
			return
		}
		resp.TabularDocuments[name] = path
	}
}

func tabularAttribute(form *metadata.FormModule, entity *metadata.Entity, name string) bool {
	if form == nil {
		return false
	}
	for _, a := range form.Attributes {
		if a != nil && strings.EqualFold(a.Name, name) && !a.Save && (strings.EqualFold(a.TypeRef, "ТабличныйДокумент") || strings.EqualFold(a.TypeRef, "SpreadsheetDocument")) {
			_, exists := entityFieldByName(entity, name)
			return !exists
		}
	}
	return false
}

func (s *Server) runTabularReport(w http.ResponseWriter, r *http.Request, rep *reportpkg.Report, params map[string]any, rows []map[string]any) error {
	decl := s.reg.GetProcedure(rep.Name, rep.ResultProc)
	if decl == nil {
		return fmt.Errorf("процедура результата отчёта не найдена")
	}
	vars, tx := s.buildDSLVarsTx(r.Context(), runtime.NewMovementsCollector("report", uuid.Nil))
	defer rollbackDSLExecution(tx)
	for name, fn := range interpreter.NewSpreadsheetFunctions() {
		vars[name] = fn
	}
	vars["Параметры"] = &interpreter.MapThis{M: params}
	resultRows := &interpreter.Array{}
	for _, row := range rows {
		resultRows.CallMethod("добавить", []any{interpreter.NewStructFromMap(row)})
	}
	vars["Результат"] = resultRows
	installTabularLayouts(vars, &tabularLayouts{s: s, owner: rep.Name})
	var result any
	err := s.interp.RunSandboxed(decl, &interpreter.MapThis{M: params}, interpreter.SandboxProfile{Context: r.Context(), MaxWallClock: 30 * time.Second}, &result, vars)
	if err = finishDSLExecution(tx, err); err != nil {
		return err
	}
	doc, ok := result.(*interpreter.SpreadsheetDocument)
	if !ok || doc == nil {
		return fmt.Errorf("процедура должна возвращать ТабличныйДокумент")
	}
	path, err := s.publishTabular(w, r, tabularScope{Kind: "report", Name: rep.Name}, doc.Doc)
	if err != nil {
		return err
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
	return nil
}
