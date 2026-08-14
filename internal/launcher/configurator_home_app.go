package launcher

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/ui"
	"gopkg.in/yaml.v3"
)

func homeWidgetsNames(hp *metadata.HomePage) []string {
	if hp == nil {
		return nil
	}
	var names []string
	for _, row := range hp.Rows {
		names = append(names, row.Widgets...)
	}
	for _, w := range hp.Widgets {
		names = append(names, w.Name)
	}
	return names
}

// homeLayoutMode возвращает режим раскладки для селектора: "rows" или "auto".
func homeLayoutMode(hp *metadata.HomePage) string {
	if hp != nil && hp.Layout == "rows" {
		return "rows"
	}
	return "auto"
}

// rowsFromForm строит ряды виджетов и режим раскладки из формы редактора.
// Режим «По рядам» (home_layout=rows) читает JSON home_rows из drag-конструктора;
// иначе отмеченные галочками виджеты (home_widgets) складываются в один ряд.
func rowsFromForm(r *http.Request) ([]metadata.HomePageRow, string) {
	clean := func(in []string) []string {
		var out []string
		for _, n := range in {
			if n = strings.TrimSpace(n); n != "" {
				out = append(out, n)
			}
		}
		return out
	}
	if r.FormValue("home_layout") == "rows" {
		var raw [][]string
		_ = json.Unmarshal([]byte(r.FormValue("home_rows")), &raw)
		var rows []metadata.HomePageRow
		for _, names := range raw {
			if c := clean(names); len(c) > 0 {
				rows = append(rows, metadata.HomePageRow{Widgets: c})
			}
		}
		return rows, "rows"
	}
	if names := clean(r.Form["home_widgets"]); len(names) > 0 {
		return []metadata.HomePageRow{{Widgets: names}}, "auto"
	}
	return nil, "auto"
}

func (h *handler) configuratorSaveSubsystem(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if failForm(w, r) {
		return
	}

	subName := strings.TrimSpace(r.FormValue("subsystem_name"))
	title := r.FormValue("title")
	icon := ui.NormalizeIconName(r.FormValue("icon"))
	orderStr := r.FormValue("order")
	// Порядок из формы. Раньше Sscanf молча оставлял 0 на любом мусоре, и
	// подсистема уезжала в начало списка так, будто пользователь сам это
	// задал. Пустое поле — по-прежнему 0, это осмысленное «без порядка».
	var order int
	if orderStr != "" {
		n, cerr := strconv.Atoi(strings.TrimSpace(orderStr))
		if cerr != nil {
			data := h.loadCfgData(r.Context(), b, "tree")
			data.Error = tr(lang, "Порядок должен быть целым числом") + ": " + orderStr
			renderCfg(w, r, data)
			return
		}
		order = n
	}

	// Без имени подсистему не сохраняем — иначе на диске появляется битый
	// файл «.yaml» с пустым name (пустая подсистема в дереве).
	if subName == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Укажите имя подсистемы")
		renderCfg(w, r, data)
		return
	}

	if title == "" {
		title = subName
	}

	relPath := "subsystems/" + nameToFilename(subName) + ".yaml"

	// Точечная правка YAML вместо пересборки файла из struct.
	//
	// Прежде файл собирался полным yaml.Marshal локальной struct, и всё, чего в
	// ней нет — незнакомые ключи (нынешние и будущие) и любые комментарии, —
	// молча исчезало при первом же сохранении из конфигуратора. Ровно этот
	// антипаттерн уже дважды чинили: в config/app.yaml (#663) и в матрице ролей
	// (#744). Часть данных struct пыталась спасать вручную, перечитывая файл и
	// перенося roles/pages/titles/home_page обратно, — то есть список
	// «что не потерять» приходилось вести руками, и он неизбежно отставал (#878).
	raw, _ := h.readConfigFileRaw(r.Context(), b, relPath)
	out, err := updateYAMLMapping(raw, relPath, func(doc *yaml.Node) error {
		if err := setAppYAMLFields(doc, []appYAMLField{
			{key: "name", val: subName},
			{key: "title", val: title},
			{key: "icon", val: strOrNil(icon)},
			{key: "order", val: order},
		}); err != nil {
			return err
		}
		// Переводы правим только когда форма их прислала: иначе они остаются
		// в файле как были.
		if formHasMapField(r, "titles") {
			titles := parseMapForm(r, "titles")
			var val any
			if len(titles) > 0 {
				val = titles
			}
			if err := setYAMLMapField(doc, "titles", val); err != nil {
				return err
			}
		}

		contents, err := yamlSubMap(doc, "contents")
		if err != nil {
			return err
		}
		// pages форма не редактирует — ключ не трогаем вовсе, он останется
		// в файле сам по себе, без переноса руками.
		for _, sec := range []struct {
			key   string
			field string
		}{
			{"catalogs", "catalogs"},
			{"documents", "documents"},
			{"registers", "registers"},
			{"inforegs", "inforegs"},
			{"reports", "reports"},
			{"processors", "processors"},
			{"journals", "journals"},
		} {
			var val any
			if list := r.Form[sec.field]; len(list) > 0 {
				val = list
			}
			if err := setYAMLMapField(contents, sec.key, val); err != nil {
				return err
			}
		}

		// Раскладка виджетов рабочего стола: режим «Авто» — отмеченные
		// галочками виджеты одним рядом; «По рядам» — ряды из drag-конструктора.
		// Правятся только rows/layout/widgets; title и titles рабочего стола
		// остаются как в файле.
		rows, layout := rowsFromForm(r)
		if len(rows) > 0 {
			home, err := yamlSubMap(doc, "home_page")
			if err != nil {
				return err
			}
			if err := setYAMLMapField(home, "rows", rows); err != nil {
				return err
			}
			if err := setYAMLMapField(home, "layout", strOrNil(layout)); err != nil {
				return err
			}
			// rows и плоский список виджетов взаимоисключающи.
			return setYAMLMapField(home, "widgets", nil)
		}
		if home, err := yamlSubMap(doc, "home_page"); err == nil {
			if err := setYAMLMapField(home, "rows", nil); err != nil {
				return err
			}
			// Пустой рабочий стол убираем целиком, чтобы не оставлять
			// «home_page: {}».
			if len(home.Content) == 0 {
				return setYAMLMapField(doc, "home_page", nil)
			}
		}
		return nil
	})
	if err == nil {
		err = saveConfigFile(r, h, b, relPath, out)
	}
	data := h.loadCfgData(r.Context(), b, "tree")
	if err != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	data.FieldsSaved = true
	data.FieldsSavedEntity = subName
	renderCfg(w, r, data)
}

// ── App config save ───────────────────────────────────────────────────────────

func (h *handler) configuratorSaveApp(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Parse multipart form (up to 2 MiB for the logo plus framing).
	lang := resolveLang(r)
	const maxLogoBytes = int64(2 << 20)
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoBytes+(1<<20))
	if err := r.ParseMultipartForm(maxLogoBytes); err != nil {
		http.Error(w, tr(lang, "Ошибка разбора формы")+": "+err.Error(), requestBodyErrorStatus(err))
		return
	}
	newName := strings.TrimSpace(r.FormValue("app_name"))
	newVersion := strings.TrimSpace(r.FormValue("app_version"))
	newLang := strings.TrimSpace(r.FormValue("app_lang"))
	newAuthor := strings.TrimSpace(r.FormValue("app_author"))
	newCopyright := strings.TrimSpace(r.FormValue("app_copyright"))
	newLicense := strings.TrimSpace(r.FormValue("app_license"))
	newSupport := strings.TrimSpace(r.FormValue("app_support"))
	existingLogo := strings.TrimSpace(r.FormValue("app_logo_existing"))
	removeLogo := r.FormValue("app_logo_remove") == "1"

	if newName == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя конфигурации не может быть пустым")
		renderCfg(w, r, data)
		return
	}

	// Determine logo path
	logoPath := existingLogo
	if removeLogo {
		logoPath = ""
	}

	// Handle uploaded logo file. The write itself is delayed so app.yaml and
	// logo changes become one config version in database mode.
	var (
		logoData      []byte
		hasLogoUpload bool
	)
	file, header, ferr := r.FormFile("app_logo_file")
	if ferr == nil {
		defer closeRead("загруженный логотип", file)
		data, rerr := io.ReadAll(io.LimitReader(file, maxLogoBytes+1))
		if rerr != nil {
			data := h.loadCfgData(r.Context(), b, "tree")
			data.Error = tr(lang, "Ошибка чтения логотипа") + ": " + rerr.Error()
			renderCfg(w, r, data)
			return
		}
		if int64(len(data)) > maxLogoBytes {
			data := h.loadCfgData(r.Context(), b, "tree")
			data.Error = tr(lang, "Логотип слишком большой (максимум 2 МБ)")
			renderCfg(w, r, data)
			return
		}
		// Determine storage path
		ext := strings.ToLower(filepath.Ext(header.Filename))
		if ext == "" {
			ext = ".png"
		}
		logoPath = "config/logo" + ext
		logoData = data
		hasLogoUpload = true
	}

	// Правим только ключи этой формы. Сборка файла заново из структуры на восемь
	// полей стирала всё остальное — email, webhooks, llm, backup вместе с ключами
	// доступа S3, limits, db (issue #656).
	rawApp, _ := h.readConfigFileRaw(r.Context(), b, appConfigPath)
	out, appErr := updateAppYAML(rawApp, func(doc *yaml.Node) error {
		return setAppYAMLFields(doc, []appYAMLField{
			{"name", newName}, // пустое имя отсечено выше
			{"version", strOrNil(newVersion)},
			{"lang", strOrNil(newLang)},
			{"logo", strOrNil(logoPath)},
			{"author", strOrNil(newAuthor)},
			{"copyright", strOrNil(newCopyright)},
			{"license", strOrNil(newLicense)},
			{"support", strOrNil(newSupport)},
		})
	})
	if appErr != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка сохранения") + ": " + appErr.Error()
		renderCfg(w, r, data)
		return
	}

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			repo := configdb.New(db)
			if err := repo.EnsureSchema(r.Context()); err != nil {
				saveErr = err
			} else {
				saves := []configdb.ConfigFile{{Path: "config/app.yaml", Content: out}}
				if hasLogoUpload {
					saves = append([]configdb.ConfigFile{{Path: logoPath, Content: logoData}}, saves...)
				}
				var deletes []string
				if removeLogo && existingLogo != "" {
					deletes = append(deletes, existingLogo)
				}
				saveErr = repo.ApplyFiles(r.Context(), saves, deletes, configdb.VersionOptions{
					AuthorLogin: cfgLogin(r.Context()),
					Message:     "save app settings",
				})
			}
		}
	} else {
		if removeLogo && existingLogo != "" {
			full, err := configdb.SafeJoin(b.Path, existingLogo)
			if err != nil {
				saveErr = err
			} else if err := os.Remove(full); err != nil && !os.IsNotExist(err) { //nolint:gosec // G703: full построен configdb.SafeJoin
				saveErr = err
			}
		}
		if saveErr == nil && hasLogoUpload {
			saveErr = h.writeConfigFileRaw(r.Context(), b, logoPath, logoData)
		}
		if saveErr == nil {
			saveErr = h.writeConfigFileRaw(r.Context(), b, "config/app.yaml", out)
		}
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "__app__"
	}
	renderCfg(w, r, data)
}

// ── InfoRegister field save ───────────────────────────────────────────────────
