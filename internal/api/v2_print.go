package api

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/sheet"
	"github.com/ivantit66/onebase/internal/storage"
)

// printDocumentV2 renders declarative document forms through the UI service.
// DSL/module printing and dispatch to a printer are outside this REST contract.
func (h *handler) printDocumentV2(pdf bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, name, ok := h.entityFromV2Route(w, r, metadata.KindDocument)
		if !ok || !requireRESTPerm(w, r, metadata.KindDocument, name, "read") {
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}
		form := chi.URLParam(r, "form")
		// chi routes on RawPath when present; mounted RoutePath inherits
		// that encoding. Otherwise it uses the already decoded URL.Path.
		if r.URL.RawPath != "" {
			form, err = url.PathUnescape(form)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid form name", "", 0)
				return
			}
		}
		ref, ok := h.reg.GetPrintFormRef(entity.Name, form)
		if !ok || ref.Kind != runtime.PrintFormDeclarative || ref.Decl == nil {
			writeError(w, http.StatusNotFound, "unknown declarative print form", "", 0)
			return
		}
		var doc *sheet.Document
		var renderCtx *printform.RenderContext
		rowDenied := errors.New("forbidden")
		// Authorisation and print data must see the same row, even if its
		// owner changes between the RLS check and the renderer's reads.
		err = h.store.WithReadSnapshot(r.Context(), func(ctx context.Context) error {
			if !h.rowAllowedID(ctx, entity, "read", id) {
				return rowDenied
			}
			var err error
			doc, renderCtx, err = h.printForms.BuildDeclarativeSheet(r.WithContext(ctx), entity, id, ref.Decl)
			return err
		})
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, rowDenied) {
				status = http.StatusForbidden
			} else if storage.IsNotFound(err) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error(), "", 0)
			return
		}
		if !pdf {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(doc.HTML(sheet.HTMLOptions{})))
			return
		}
		data, err := doc.PDF(sheet.PDFOptions{Title: ref.Name})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		filename := ref.Name
		if number, _ := renderCtx.Document["Номер"].(string); number != "" {
			filename += "_" + number
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename + ".pdf"}))
		_, _ = w.Write(data)
	}
}

func printFormPath(pdf bool, nameParam, idParam, errorResponses map[string]any) map[string]any {
	contentType, schema, operation := "text/html", map[string]any{"type": "string"}, "printDocumentHTML"
	if pdf {
		contentType, schema, operation = "application/pdf", map[string]any{"type": "string", "format": "binary"}, "printDocumentPDF"
	}
	return map[string]any{"get": map[string]any{
		"operationId": operation,
		"summary":     "Render a declarative document print form",
		"tags":        []string{"document"},
		"parameters": []any{nameParam, idParam, map[string]any{
			"name": "form", "in": "path", "required": true, "schema": map[string]any{"type": "string"},
		}},
		"responses": mergeResponses(map[string]any{"200": map[string]any{
			"description": "Rendered print form (read permission, row access and field masks apply)",
			"content":     map[string]any{contentType: map[string]any{"schema": schema}},
		}}, errorResponses),
	}}
}
