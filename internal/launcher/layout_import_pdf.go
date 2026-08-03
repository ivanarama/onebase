package launcher

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/pdfimport"
)

// ── Импорт макета из PDF (план 64, этап 6, фаза 2) ──────────────────────────
//
// «Создать макет из PDF» рядом с «+ Печатная форма (макет)»: пользователь
// выбирает PDF (выгрузка 1С/Excel с текстовым слоем), задаёт имя формы и номер
// страницы. ImportPage извлекает черновик-скелет (сетка, тексты, спаны, границы),
// который сохраняется как printforms/<имя>.layout.yaml и открывается в редакторе.
//
// Запись переиспользует механику layout_new.go (file-mode + configdb).
// Недоверенный ввод: MaxBytesReader на 10МБ + запас; парсер pdfimport сам под
// recover/таймаутом/лимитом.

// maxPDFUpload — верхняя граница тела запроса (лимит файла 10МБ + запас на
// multipart-обёртку и поля формы).
const maxPDFUpload = pdfimport.MaxFileSize + (1 << 20)

// configuratorImportPDFLayout обрабатывает POST .../configurator/layout/import-pdf.
// Поля multipart-формы: file (PDF), name (имя макета), page (номер страницы, 1+).
func (h *handler) configuratorImportPDFLayout(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)

	r.Body = http.MaxBytesReader(w, r.Body, maxPDFUpload)
	if err := r.ParseMultipartForm(maxPDFUpload); err != nil {
		if requestBodyErrorStatus(err) == http.StatusRequestEntityTooLarge {
			http.Error(w, tr(lang, "Файл слишком большой или форма повреждена"), http.StatusRequestEntityTooLarge)
			return
		}
		h.layoutCreateError(w, r, b, lang, tr(lang, "Файл слишком большой или форма повреждена"))
		return
	}

	layoutName := strings.TrimSpace(r.FormValue("name"))
	if layoutName == "" {
		h.layoutCreateError(w, r, b, lang, tr(lang, "Имя макета обязательно"))
		return
	}
	if !validLayoutName(layoutName) {
		h.layoutCreateError(w, r, b, lang, tr(lang, "Недопустимое имя файла"))
		return
	}
	// Привязка к документу обязательна: без document: форма не попадает в
	// список печати (runtime индексирует декларативные формы по Document).
	document := strings.TrimSpace(r.FormValue("document"))
	if document == "" {
		h.layoutCreateError(w, r, b, lang, tr(lang, "Для макета выберите документ/справочник"))
		return
	}

	page := 1
	if p := strings.TrimSpace(r.FormValue("page")); p != "" {
		if n, perr := strconv.Atoi(p); perr == nil && n >= 1 {
			page = n
		}
	}

	file, _, ferr := r.FormFile("file")
	if ferr != nil {
		h.layoutCreateError(w, r, b, lang, tr(lang, "Выберите PDF-файл"))
		return
	}
	defer closeRead("загруженный PDF", file)

	var buf bytes.Buffer
	if _, cerr := io.Copy(&buf, file); cerr != nil {
		h.layoutCreateError(w, r, b, lang, tr(lang, "Не удалось прочитать файл"))
		return
	}

	lt, ierr := pdfimport.ImportBytes(buf.Bytes(), page)
	if ierr != nil {
		h.layoutCreateError(w, r, b, lang, importPDFErrorMessage(lang, ierr))
		return
	}
	lt.Name = layoutName
	lt.Document = document

	src, merr := marshalLayout(lt)
	if merr != nil {
		h.layoutCreateError(w, r, b, lang, tr(lang, "Ошибка создания макета")+": "+merr.Error())
		return
	}

	filename := layoutName + ".layout.yaml"
	relPath := "printforms/" + filename

	if b.ConfigSource == "database" {
		db, derr := OpenDB(r.Context(), b)
		if derr != nil {
			h.layoutCreateError(w, r, b, lang, tr(lang, "Ошибка создания макета")+": "+derr.Error())
			return
		}
		defer db.Close()
		repo := configdb.New(db)
		if _, ok, _ := repo.ReadFile(r.Context(), relPath); ok {
			h.layoutCreateError(w, r, b, lang, tr(lang, "Макет уже существует"))
			return
		}
		if werr := repo.SaveFile(r.Context(), relPath, src); werr != nil {
			h.layoutCreateError(w, r, b, lang, tr(lang, "Ошибка создания макета")+": "+werr.Error())
			return
		}
	} else {
		fullPath, jerr := configdb.SafeJoin(b.Path, relPath)
		if jerr != nil {
			h.layoutCreateError(w, r, b, lang, tr(lang, "Ошибка создания макета")+": "+jerr.Error())
			return
		}
		if _, statErr := os.Stat(fullPath); statErr == nil { //nolint:gosec // G703: fullPath построен configdb.SafeJoin
			h.layoutCreateError(w, r, b, lang, tr(lang, "Макет уже существует"))
			return
		}
		if merr := os.MkdirAll(filepath.Dir(fullPath), 0o755); merr != nil { //nolint:gosec // G301: права — соглашение пакета, разбор на этапе 109H
			h.layoutCreateError(w, r, b, lang, tr(lang, "Ошибка создания макета")+": "+merr.Error())
			return
		}
		if werr := os.WriteFile(fullPath, src, 0o644); werr != nil { //nolint:gosec // G306: то же
			h.layoutCreateError(w, r, b, lang, tr(lang, "Ошибка создания макета")+": "+werr.Error())
			return
		}
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	data.FieldsSaved = true
	data.FieldsSavedEntity = layoutName
	data.SavedMessage = tr(lang, "✓ Макет") + " «" + layoutName + "» " + tr(lang, "создан из PDF — черновик открыт в редакторе. Перезапустите базу, чтобы форма появилась в списке печати.")
	data.SelectedTreeID = "mkt-" + layoutName
	renderCfg(w, r, data)
}

// importPDFErrorMessage переводит ошибку pdfimport в понятное пользователю
// сообщение (t-ключи en+de) с сохранением деталей парсера.
func importPDFErrorMessage(lang string, err error) string {
	switch {
	case errors.Is(err, pdfimport.ErrNoTextLayer):
		return tr(lang, "В PDF не найден текстовый слой: похоже, это скан или изображение. Импорт макета возможен только для PDF с текстом (выгрузка из 1С/Excel).")
	case errors.Is(err, pdfimport.ErrPageNotFound):
		return tr(lang, "Указанной страницы нет в документе.")
	case errors.Is(err, pdfimport.ErrFileTooLarge):
		return tr(lang, "Файл больше 10 МБ — слишком большой для импорта.")
	case errors.Is(err, pdfimport.ErrParse):
		return tr(lang, "Не удалось разобрать PDF (возможно, файл повреждён или зашифрован).")
	default:
		return tr(lang, "Ошибка импорта PDF") + ": " + err.Error()
	}
}
