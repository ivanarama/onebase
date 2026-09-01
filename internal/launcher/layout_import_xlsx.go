package launcher

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/xlsximport"
)

// ── Импорт макета из Excel (план 155, обсуждение #1109) ─────────────────────
//
// «Из Excel» рядом с «Из PDF»: пользователь рисует бланк в Excel — там дешевле
// расставить объединения, ширины и границы, чем в YAML-редакторе, — пишет в
// ячейки те же теги ({{Номер}}, {{Товары.Количество}}), а импорт превращает
// лист в printforms/<имя>.layout.yaml. Дальше это обычный макет.
//
// Отличие от импорта PDF: чтобы отличить колонку табличной части от поля по
// ссылке, нужны метаданные — список ТЧ выбранного документа берётся из
// конфигурации (findEntity) и передаётся импортёру.

// maxXLSXUpload — верхняя граница тела запроса (лимит файла + запас на
// multipart-обёртку и поля формы).
const maxXLSXUpload = xlsximport.MaxFileSize + (1 << 20)

// configuratorImportXLSXLayout обрабатывает POST .../configurator/layout/import-xlsx.
// Поля multipart-формы: file (.xlsx), name (имя макета), document (привязка),
// sheet (имя листа, необязательно).
func (h *handler) configuratorImportXLSXLayout(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)

	r.Body = http.MaxBytesReader(w, r.Body, maxXLSXUpload)
	if perr := r.ParseMultipartForm(maxXLSXUpload); perr != nil {
		if requestBodyErrorStatus(perr) == http.StatusRequestEntityTooLarge {
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
	// список печати, и без неё же неизвестен состав табличных частей.
	document := strings.TrimSpace(r.FormValue("document"))
	if document == "" {
		h.layoutCreateError(w, r, b, lang, tr(lang, "Для макета выберите документ/справочник"))
		return
	}

	file, _, ferr := r.FormFile("file")
	if ferr != nil {
		h.layoutCreateError(w, r, b, lang, tr(lang, "Выберите файл Excel (.xlsx)"))
		return
	}
	defer closeRead("загруженный XLSX", file)

	var buf bytes.Buffer
	if _, cerr := io.Copy(&buf, file); cerr != nil {
		h.layoutCreateError(w, r, b, lang, tr(lang, "Не удалось прочитать файл"))
		return
	}

	res, ierr := xlsximport.ImportBytes(buf.Bytes(), xlsximport.Options{
		Sheet:      strings.TrimSpace(r.FormValue("sheet")),
		TableParts: h.entityTableParts(r, b, document),
	})
	if ierr != nil {
		h.layoutCreateError(w, r, b, lang, importXLSXErrorMessage(lang, ierr))
		return
	}
	res.Layout.Name = layoutName
	res.Layout.Document = document

	src, merr := marshalLayout(res.Layout)
	if merr != nil {
		h.layoutCreateError(w, r, b, lang, tr(lang, "Ошибка создания макета")+": "+merr.Error())
		return
	}

	relPath := "printforms/" + layoutName + ".layout.yaml"
	if werr := h.writeLayoutFile(r.Context(), b, relPath, src); werr != nil {
		h.layoutCreateError(w, r, b, lang, layoutWriteMessage(lang, werr))
		return
	}
	templatePath := "printforms/" + layoutName + ".template.xlsx"
	if werr := h.writeLayoutFile(r.Context(), b, templatePath, buf.Bytes()); werr != nil {
		// Макет и исходная книга образуют одну печатную форму. Если второй файл
		// записать не удалось, не оставляем половину импорта.
		msg := layoutWriteMessage(lang, werr)
		if rerr := h.removeLayoutFile(r.Context(), b, relPath); rerr != nil {
			msg += ". " + tr(lang, "Не удалось удалить незавершённый макет") + ": " + rerr.Error()
		}
		h.layoutCreateError(w, r, b, lang, msg)
		return
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	data.FieldsSaved = true
	data.FieldsSavedEntity = layoutName
	data.SavedMessage = tr(lang, "✓ Макет") + " «" + layoutName + "» " +
		tr(lang, "создан из Excel — черновик открыт в редакторе. Перезапустите базу, чтобы форма появилась в списке печати.")
	if notes := res.Warnings; len(notes) > 0 {
		data.SavedMessage += " " + tr(lang, "Перенесено не всё:") + " " + strings.Join(notes, " ")
	}
	data.SelectedTreeID = "mkt-" + layoutName
	renderCfg(w, r, data)
}

// entityTableParts возвращает имена табличных частей сущности. Пустой список —
// импорт не станет разворачивать строки ТЧ и честно скажет об этом.
func (h *handler) entityTableParts(r *http.Request, b *Base, entityName string) []string {
	ent := h.findEntity(r, b, entityName)
	if ent == nil {
		return nil
	}
	names := make([]string, 0, len(ent.TableParts))
	for _, tp := range ent.TableParts {
		names = append(names, tp.Name)
	}
	return names
}

// importXLSXErrorMessage переводит ошибку импортёра в сообщение пользователю.
func importXLSXErrorMessage(lang string, err error) string {
	switch {
	case errors.Is(err, xlsximport.ErrFileTooLarge):
		return tr(lang, "Файл больше 5 МБ — слишком большой для импорта.")
	case errors.Is(err, xlsximport.ErrSheetNotFound):
		return tr(lang, "Лист с таким именем не найден в книге.") + " " + err.Error()
	case errors.Is(err, xlsximport.ErrEmptySheet):
		return tr(lang, "Лист пуст — импортировать нечего.")
	case errors.Is(err, xlsximport.ErrParse):
		return tr(lang, "Не удалось прочитать файл Excel (возможно, он повреждён, защищён паролем или это не .xlsx).")
	default:
		return tr(lang, "Ошибка импорта Excel") + ": " + err.Error()
	}
}

// errLayoutExists — макет с таким именем уже есть; перезаписывать молча нельзя.
var errLayoutExists = errors.New("макет уже существует")

// layoutWriteMessage переводит ошибку записи макета в сообщение пользователю.
func layoutWriteMessage(lang string, err error) string {
	if errors.Is(err, errLayoutExists) {
		return tr(lang, "Макет уже существует")
	}
	return tr(lang, "Ошибка создания макета") + ": " + err.Error()
}

// writeLayoutFile сохраняет новый макет — на диск или в конфигурацию в БД, в
// зависимости от режима базы. Существующий файл не перезаписывается.
//
// Общий код для всех путей создания макета (импорт из PDF и из Excel): раньше
// эти полсотни строк с двумя ветками хранилища и guard-ом от traversal стояли
// в каждом обработчике своей копией.
func (h *handler) writeLayoutFile(ctx context.Context, b *Base, relPath string, src []byte) error {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return err
		}
		defer db.Close()
		repo := configdb.New(db)
		if _, ok, _ := repo.ReadFile(ctx, relPath); ok {
			return errLayoutExists
		}
		return repo.SaveFile(ctx, relPath, src)
	}

	fullPath, err := configdb.SafeJoin(b.Path, relPath)
	if err != nil {
		return err
	}
	if _, serr := os.Stat(fullPath); serr == nil { //nolint:gosec // G703: fullPath построен configdb.SafeJoin — это и есть guard от traversal
		return errLayoutExists
	}
	if merr := os.MkdirAll(filepath.Dir(fullPath), fsmode.Dir); merr != nil { //nolint:gosec // G703: fullPath построен configdb.SafeJoin
		return merr
	}
	return os.WriteFile(fullPath, src, fsmode.File) //nolint:gosec // G703: fullPath построен configdb.SafeJoin
}

func (h *handler) removeLayoutFile(ctx context.Context, b *Base, relPath string) error {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return err
		}
		defer db.Close()
		return configdb.New(db).DeleteFile(ctx, relPath)
	}
	fullPath, err := configdb.SafeJoin(b.Path, relPath)
	if err != nil {
		return err
	}
	return os.Remove(fullPath) //nolint:gosec // G703: fullPath построен configdb.SafeJoin
}
