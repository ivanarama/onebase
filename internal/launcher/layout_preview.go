package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/sheet"
	"github.com/ivantit66/onebase/internal/storage"
)

// layout_preview.go — предпросмотр макета печатной формы в дизайнере
// (план 64, этап 5b, пункт 6.6). POST /configurator/layout/preview принимает
// {yaml, entity}, парсит макет v2, собирает данные (последняя запись сущности из
// БД базы или синтетика) и рендерит BuildSheet → HTML (или PDF при ?format=pdf).

// maxPreviewBody — лимит размера тела запроса предпросмотра (защита от больших YAML).
const maxPreviewBody = 1 << 20 // 1 MiB

// layoutPreviewReq — тело запроса предпросмотра.
type layoutPreviewReq struct {
	YAML   string `json:"yaml"`
	Entity string `json:"entity"`
}

// configuratorLayoutPreview рендерит предпросмотр макета. format=pdf → PDF inline.
func (h *handler) configuratorLayoutPreview(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPreviewBody)
	var req layoutPreviewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Парсинг макета под recover (yaml.v3 на кривом вводе может паниковать в
	// кастомном UnmarshalYAML).
	lt, perr := parseLayoutSafe([]byte(req.YAML))
	if perr != nil {
		http.Error(w, "layout parse error: "+perr.Error(), http.StatusBadRequest)
		return
	}
	if lt == nil || len(lt.Areas) == 0 {
		http.Error(w, "layout has no areas", http.StatusBadRequest)
		return
	}

	// Имя сущности: из запроса, иначе из document: макета.
	entityName := strings.TrimSpace(req.Entity)
	if entityName == "" {
		entityName = lt.Document
	}

	ctx := h.buildPreviewContext(r, b, entityName)

	doc, berr := printform.BuildSheet(lt, ctx)
	if berr != nil {
		http.Error(w, "build error: "+berr.Error(), http.StatusInternalServerError)
		return
	}

	if r.URL.Query().Get("format") == "pdf" {
		pdfBytes, err := doc.PDF(sheet.PDFOptions{Title: lt.Name})
		if err != nil {
			http.Error(w, "PDF error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// open=1 → сохранить во временный файл и открыть системным приложением.
		// В GUI-сборке (WebView2) нет встроенного PDF-просмотрщика — inline-iframe
		// показывает «страница заблокирована Microsoft Edge». Внешнее открытие
		// работает и в webview, и в обычном браузере (лаунчер локальный).
		if r.URL.Query().Get("open") == "1" {
			fp := filepath.Join(os.TempDir(), "onebase-layout-preview.pdf")
			if werr := os.WriteFile(fp, pdfBytes, 0o600); werr != nil {
				// Старый файл занят просмотрщиком → уникальное имя.
				tf, terr := os.CreateTemp("", "onebase-preview-*.pdf")
				if terr != nil {
					http.Error(w, "PDF temp error: "+werr.Error(), http.StatusInternalServerError)
					return
				}
				tf.Write(pdfBytes)
				fp = tf.Name()
				tf.Close()
			}
			if oerr := OpenPath(fp); oerr != nil {
				http.Error(w, "open error: "+oerr.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			writeBody(w, []byte(`{"ok":true}`))
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `inline; filename="preview.pdf"`)
		writeBody(w, pdfBytes)
		return
	}

	html := doc.HTML(sheet.HTMLOptions{})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeBody(w, []byte(html))
}

// parseLayoutSafe парсит макет v2 под recover (паника → ошибка).
func parseLayoutSafe(data []byte) (lt *printform.LayoutTemplate, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			lt = nil
			err = fmt.Errorf("panic: %v", rec)
		}
	}()
	return printform.ParseLayoutBytes(data)
}

// buildPreviewContext собирает RenderContext для предпросмотра: пытается взять
// последнюю запись сущности из БД базы; при недоступности БД/сущности/записей —
// синтетические данные (поля = «<Имя>», ТЧ = 3 строки-заглушки).
// Проект грузится ОДИН раз: сущность и индекс ссылок берутся из одного объекта.
func (h *handler) buildPreviewContext(r *http.Request, b *Base, entityName string) *printform.RenderContext {
	ctx := r.Context()

	// Однократная загрузка проекта.
	proj, err := h.loadProjectFor(ctx, b)
	if err != nil {
		// нет метаданных — минимальная синтетика без структуры.
		return &printform.RenderContext{
			Document:   map[string]any{"Номер": "000000001", "Дата": "01.01.2025"},
			TableParts: map[string][]map[string]any{},
		}
	}
	defer proj.Close()

	// Индекс сущностей из уже загруженного проекта.
	refEntities := make(map[string]*metadata.Entity, len(proj.Entities))
	for _, e := range proj.Entities {
		refEntities[strings.ToLower(e.Name)] = e
	}

	// Найти целевую сущность по имени (регистронезависимо).
	var ent *metadata.Entity
	if entityName != "" {
		ent = refEntities[strings.ToLower(entityName)]
	}
	if ent == nil {
		// нет метаданных — минимальная синтетика без структуры.
		return &printform.RenderContext{
			Document:   map[string]any{"Номер": "000000001", "Дата": "01.01.2025"},
			TableParts: map[string][]map[string]any{},
		}
	}

	if rctx := h.loadLastRecordContext(ctx, b, ent, refEntities); rctx != nil {
		return rctx
	}
	return syntheticContext(ent)
}

// loadLastRecordContext загружает последнюю запись сущности и её ТЧ/ссылки/константы.
// refEntities — индекс из уже загруженного проекта (проект НЕ грузится повторно).
// Возвращает nil, если БД недоступна или записей нет (вызывающий → синтетика).
func (h *handler) loadLastRecordContext(ctx context.Context, b *Base, ent *metadata.Entity, refEntities map[string]*metadata.Entity) *printform.RenderContext {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.List(ctx, ent.Name, ent, storage.ListParams{Limit: 1, Dir: "desc"})
	if err != nil || len(rows) == 0 {
		return nil
	}
	row := rows[0]

	idStr, _ := row["id"].(string)
	id, perr := uuid.Parse(idStr)

	tpRows := make(map[string][]map[string]any)
	if perr == nil {
		for _, tp := range ent.TableParts {
			trs, _ := db.GetTablePartRows(ctx, ent.Name, tp.Name, id, tp)
			tpRows[tp.Name] = trs
		}
	}

	refs := buildPreviewRefs(ctx, db, row, ent, tpRows, refEntities)
	constants, _ := db.ListConstants(ctx)

	return &printform.RenderContext{
		Document:       row,
		TableParts:     tpRows,
		Constants:      constants,
		Refs:           refs,
		RichTextFields: printform.RichTextFields(ent),
	}
}

// buildPreviewRefs резолвит ссылочные поля записи и строк ТЧ в map UUID→поля
// (для отображения наименований в предпросмотре). Аналог ui.buildPrintRefs.
func buildPreviewRefs(ctx context.Context, db *storage.DB, row map[string]any, ent *metadata.Entity, tpRows map[string][]map[string]any, refEntities map[string]*metadata.Entity) map[string]map[string]any {
	refs := make(map[string]map[string]any)
	resolve := func(refEntityName, idStr string) {
		if idStr == "" {
			return
		}
		if _, dup := refs[idStr]; dup {
			return
		}
		refEnt := refEntities[strings.ToLower(refEntityName)]
		if refEnt == nil {
			return
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			return
		}
		refRow, err := db.GetByID(ctx, refEnt.Name, id, refEnt)
		if err != nil {
			return
		}
		refs[idStr] = refRow
	}
	for _, f := range ent.Fields {
		if f.RefEntity == "" {
			continue
		}
		idStr, _ := row[f.Name].(string)
		resolve(f.RefEntity, idStr)
	}
	for _, tp := range ent.TableParts {
		for _, f := range tp.Fields {
			if f.RefEntity == "" {
				continue
			}
			for _, tr := range tpRows[tp.Name] {
				idStr, _ := tr[f.Name].(string)
				resolve(f.RefEntity, idStr)
			}
		}
	}
	return refs
}

// syntheticContext строит синтетический контекст по метаданным сущности: каждое
// поле документа = «<Имя>», каждая ТЧ = 3 строки-заглушки (поля = «<Имя> N»,
// числовые = N). Нужен для предпросмотра пустой/недоступной базы.
func syntheticContext(ent *metadata.Entity) *printform.RenderContext {
	doc := make(map[string]any, len(ent.Fields)+2)
	for _, f := range ent.Fields {
		doc[f.Name] = synthFieldValue(f, 0)
	}
	// Номер — узнаваемая заглушка независимо от того, объявлено ли поле.
	doc["Номер"] = "000000001"

	tpRows := make(map[string][]map[string]any, len(ent.TableParts))
	for _, tp := range ent.TableParts {
		var rows []map[string]any
		for i := 1; i <= 3; i++ {
			row := make(map[string]any, len(tp.Fields))
			for _, f := range tp.Fields {
				row[f.Name] = synthFieldValue(f, i)
			}
			rows = append(rows, row)
		}
		tpRows[tp.Name] = rows
	}

	return &printform.RenderContext{
		Document:       doc,
		TableParts:     tpRows,
		Constants:      map[string]any{},
		RichTextFields: printform.RichTextFields(ent),
	}
}

// synthFieldValue возвращает заглушку для поля: число → n, иначе «<Имя>[ n]».
func synthFieldValue(f metadata.Field, n int) any {
	t := strings.ToLower(string(f.Type))
	if strings.HasPrefix(t, "number") || strings.HasPrefix(t, "decimal") {
		if n == 0 {
			return 100
		}
		return n * 100
	}
	if n == 0 {
		return f.Name
	}
	return fmt.Sprintf("%s %d", f.Name, n)
}
