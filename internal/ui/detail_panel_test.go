package ui

// Боковая панель деталей активной записи (план 118B, issue #670).

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

var detailPanelAttrRE = regexp.MustCompile(`data-ob-detail='([^']*)'`)

func firstDetailPanelData(t *testing.T, page string) detailPanelData {
	t.Helper()
	match := detailPanelAttrRE.FindStringSubmatch(page)
	if len(match) != 2 {
		t.Fatalf("страница не содержит data-ob-detail: %s", page)
	}
	var data detailPanelData
	if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &data); err != nil {
		t.Fatalf("payload панели не разобрался: %v; raw=%s", err, match[1])
	}
	return data
}

func detailPanelValueByLabel(data detailPanelData, label string) (string, bool) {
	for _, tab := range data.Tabs {
		for _, field := range tab.Fields {
			if field.Label == label {
				return field.Value, true
			}
		}
	}
	return "", false
}

func panelEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Номенклатура",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Артикул", Type: metadata.FieldTypeString},
			{Name: "Поставщик", Type: metadata.FieldTypeString},
			{Name: "Фото", Type: metadata.FieldTypeImage},
			{Name: "Описание", Type: metadata.FieldTypeRichText},
		},
		ListForm: []string{"Наименование", "Артикул"},
	}
}

// Автокомпоновка: все реквизиты шапки, картинки и размеченный текст — на
// отдельных закладках. Панель просили ровно за этим: «смотреть все колонки
// неудобно, особенно если среди них картинки».
func TestDetailPanel_AutoLayout(t *testing.T) {
	ent := panelEntity()
	row := map[string]any{
		"Наименование": "Кресло «Гамма»",
		"Артикул":      "10041",
		"Поставщик":    "ООО «Мебель»",
		"Фото":         "img-1",
		"Описание":     "<p>Мягкое кресло</p>",
	}
	raw := detailPanelJSON(ent.Fields, row, detailPanelTitle(ent.Fields, row), nil, "ru")
	var data detailPanelData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("payload не разобрался: %v\n%s", err, raw)
	}
	if data.Title != "Кресло «Гамма»" {
		t.Errorf("заголовок карточки = %q", data.Title)
	}
	titles := make([]string, 0, len(data.Tabs))
	byTitle := map[string][]detailPanelField{}
	for _, tab := range data.Tabs {
		titles = append(titles, tab.Title)
		byTitle[tab.Title] = tab.Fields
	}
	if strings.Join(titles, ",") != "Основное,Изображения,Описание" {
		t.Fatalf("закладки = %v", titles)
	}
	// Поставщика нет в колонках списка — именно он и нужен в панели.
	var seenSupplier bool
	for _, f := range byTitle["Основное"] {
		if f.Label == "Поставщик" && f.Value == "ООО «Мебель»" {
			seenSupplier = true
		}
	}
	if !seenSupplier {
		t.Errorf("реквизит вне колонок не попал в панель: %v", byTitle["Основное"])
	}
	if len(byTitle["Изображения"]) != 1 || byTitle["Изображения"][0].Kind != "image" {
		t.Errorf("картинка не на своей закладке: %v", byTitle["Изображения"])
	}
	// Размеченный текст уезжает срезом, без тегов: полная разметка на каждую
	// строку списка раздула бы страницу.
	rich := byTitle["Описание"]
	if len(rich) != 1 || strings.Contains(rich[0].Value, "<p>") {
		t.Errorf("richtext ушёл с разметкой: %v", rich)
	}
}

func TestDetailPanel_UsesExplicitPresentationFallback(t *testing.T) {
	ent := panelEntity()
	ent.Presentation = []string{"Артикул", "Поставщик"}
	row := map[string]any{
		"id":           "11111111-1111-1111-1111-111111111111",
		"Наименование": "Старое имя",
		"Артикул":      " ",
		"Поставщик":    "Витринное имя",
	}
	var data detailPanelData
	if err := json.Unmarshal([]byte(detailPanelForEntity(ent, row, nil, "ru")), &data); err != nil {
		t.Fatal(err)
	}
	if data.Title != "Витринное имя" {
		t.Fatalf("fallback title = %q, ожидалось Витринное имя", data.Title)
	}
	row["Артикул"] = "A-1"
	if err := json.Unmarshal([]byte(detailPanelForEntity(ent, row, nil, "ru")), &data); err != nil {
		t.Fatal(err)
	}
	if data.Title != "A-1" {
		t.Fatalf("primary title = %q, ожидалось A-1", data.Title)
	}
	row["Артикул"] = " "
	row["Поставщик"] = ""
	if err := json.Unmarshal([]byte(detailPanelForEntity(ent, row, nil, "ru")), &data); err != nil {
		t.Fatal(err)
	}
	if data.Title != "" {
		t.Fatalf("empty explicit title = %q, ожидалось пустое значение без legacy fallback", data.Title)
	}
}

func TestDetailPanel_AutoTabTitlesUseResolvedLanguage(t *testing.T) {
	ent := panelEntity()
	row := map[string]any{"Артикул": "10041", "Фото": "img-1", "Описание": "text"}
	translations := map[string]string{"Основное": "General", "Изображения": "Images", "Описание": "Description"}
	raw := detailPanelJSON(ent.Fields, row, "", nil, "en", func(key string) string { return translations[key] })
	var data detailPanelData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, tab := range data.Tabs {
		titles = append(titles, tab.Title)
	}
	if got := strings.Join(titles, ","); got != "General,Images,Description" {
		t.Fatalf("auto tab titles were not translated: %s", got)
	}
}

// Пустой объект без реквизитов не даёт payload — нечего показывать.
func TestDetailPanel_EmptyWithoutFields(t *testing.T) {
	if got := detailPanelJSON(nil, map[string]any{}, "", nil, "ru"); got != "" {
		t.Errorf("ожидался пустой payload, получено %q", got)
	}
}

// Панель уезжает в разметку списка и живёт ВНЕ контейнера живого списка:
// иначе SSE-обновление пересоздало бы её, потеряв вкладку и ширину.
func TestDetailPanel_RenderedOutsideLiveContainer(t *testing.T) {
	ent := panelEntity()
	html := renderPageList(t, map[string]any{
		"Entity": ent,
		"Rows": []map[string]any{{
			"id": "1", "Наименование": "Кресло", "Артикул": "10041", "Поставщик": "ООО «Мебель»",
		}},
		"Params": storage.ListParams{}, "RefFilterOptions": map[string]any{},
		"Lang": "ru", "Total": 1, "Page": 1, "TotalPages": 1,
	})
	if !strings.Contains(html, `id="ob-detail"`) {
		t.Fatal("панель не отрисована")
	}
	// Payload в разметке строк больше нет (#860): строка несёт только адрес,
	// по которому панель грузится при открытии.
	if strings.Contains(html, "data-ob-detail='") {
		t.Error("payload панели снова уехал в разметку строки")
	}
	if !strings.Contains(html, "data-ob-id=") {
		t.Error("в строке списка нет адреса ленивой загрузки панели")
	}
	if !strings.Contains(html, "data-ob-detail-toggle") {
		t.Error("нет кнопки включения панели")
	}
	live := strings.Index(html, `data-ob-live=`)
	panel := strings.Index(html, `id="ob-detail"`)
	closeWrap := strings.Index(html, `class="ob-list-wrap"`)
	if live < 0 || panel < 0 || closeWrap < 0 {
		t.Fatalf("разметка неполна: live=%d panel=%d wrap=%d", live, panel, closeWrap)
	}
	// Панель обязана идти ПОСЛЕ закрытия карточки живого списка.
	liveCard := html[live:panel]
	if strings.Count(liveCard, `data-ob-live=`) != 1 {
		t.Error("панель попала внутрь контейнера живого списка")
	}
}

func TestDetailPanel_ConfiguredDefaultWidthReachesClient(t *testing.T) {
	ent := panelEntity()
	ent.DetailPanel = &metadata.DetailPanel{Width: 444}
	page := renderPageList(t, map[string]any{
		"Entity": ent,
		"Rows":   []map[string]any{{"id": "1", "Наименование": "Кресло"}},
		"Params": storage.ListParams{}, "RefFilterOptions": map[string]any{},
		"Lang": "ru", "Total": 1, "Page": 1, "TotalPages": 1,
	})
	if !strings.Contains(page, `data-ob-default-width="444"`) {
		t.Fatalf("configured detail_panel.width did not reach HTML: %s", page)
	}
	js := string(uiJS)
	for _, want := range []string{"data-ob-default-width", "saved === null ? configured : saved"} {
		if !strings.Contains(js, want) {
			t.Fatalf("client does not apply configured default width: missing %q", want)
		}
	}
}

// Переключатель панели общий для таблицы, плитки и дерева, поэтому каждый вид
// обязан нести один и тот же payload. До исправления атрибут был только у
// обычной таблицы: в двух остальных режимах панель всегда писала «Выберите
// строку», хотя строка была выбрана.
func TestDetailPanel_AllEntityListModesCarryLazyURL(t *testing.T) {
	ent := panelEntity()
	row := map[string]any{
		"id": "1", "Наименование": "Кресло", "Артикул": "10041", "Поставщик": "ООО «Мебель»",
		"_depth": 0,
	}
	base := map[string]any{
		"Entity": ent, "Rows": []map[string]any{row}, "TreeRows": []map[string]any{row},
		"Params": storage.ListParams{}, "RefFilterOptions": map[string]any{},
		"Lang": "ru", "Total": 1, "Page": 1, "TotalPages": 1,
	}
	for _, tc := range []struct {
		name string
		set  func(map[string]any)
	}{
		{name: "table", set: func(map[string]any) {}},
		{name: "tiles", set: func(data map[string]any) { data["TilesView"] = true }},
		{name: "tree", set: func(data map[string]any) { data["TreeView"] = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := make(map[string]any, len(base)+1)
			for key, value := range base {
				data[key] = value
			}
			tc.set(data)
			page := renderPageList(t, data)
			if !strings.Contains(page, "data-ob-id=") {
				t.Fatalf("в %s-виде у строки нет адреса панели", tc.name)
			}

		})
	}
}

// Явный состав detail_panel — контракт сущности, и панель обязана его
// соблюдать. После перевода панели на ленивую загрузку (#860) проверять это
// нужно там, где payload теперь и собирается: в хендлере.
func TestDetailPanel_HandlerHonorsExplicitComposition(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Артикул", Type: metadata.FieldTypeString},
			{Name: "Поставщик", Type: metadata.FieldTypeString},
		},
		DetailPanel: &metadata.DetailPanel{Fields: []string{"Артикул"}, FieldsSet: true},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, id, map[string]any{
		"Наименование": "Кресло", "Артикул": "10041", "Поставщик": "ООО «Мебель»",
	}, ent); err != nil {
		t.Fatal(err)
	}

	panel := fetchDetailPanel(t, s, ent, id)
	if got, ok := detailPanelValueByLabel(panel, "Артикул"); !ok || got != "10041" {
		t.Fatalf("явно перечисленного поля нет в payload: %+v", panel)
	}
	if _, ok := detailPanelValueByLabel(panel, "Поставщик"); ok {
		t.Fatalf("поле автокомпоновки просочилось в явный состав: %+v", panel)
	}
}

// Пункт 1 #1083: после записи панель обязана читать текущий снимок из БД, а не
// пустые/устаревшие значения строки, с которой открыли форму. Проверка идёт
// публичными submitEdit → detailPanelRecord, как пользовательский путь.
func TestDetailPanel_AfterEditShowsSavedValues_1083(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Номенклатура", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Вес", Type: metadata.FieldTypeNumber},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, id, map[string]any{
		"Наименование": "До правки", "Вес": "1",
	}, ent); err != nil {
		t.Fatal(err)
	}

	request := reqWithChi(http.MethodPost, "/ui/catalog/Номенклатура/"+id.String(), url.Values{
		"Наименование": {"После правки"},
		"Вес":          {"12,4"},
	}, map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()})
	response := httptest.NewRecorder()
	s.submitEdit(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("submitEdit: статус=%d, body=%s", response.Code, response.Body.String())
	}

	panel := fetchDetailPanel(t, s, ent, id)
	if got, ok := detailPanelValueByLabel(panel, "Наименование"); !ok || got != "После правки" {
		t.Fatalf("панель не показала сохранённое наименование: %+v", panel)
	}
	if got, ok := detailPanelValueByLabel(panel, "Вес"); !ok || got != "12.4" {
		t.Fatalf("панель не показала сохранённый вес: %+v", panel)
	}
}

// fetchDetailPanel зовёт публичный хендлер панели и разбирает ответ.
func fetchDetailPanel(t *testing.T, s *Server, ent *metadata.Entity, id uuid.UUID) detailPanelData {
	t.Helper()
	r := reqWithChi(http.MethodGet,
		"/ui/catalog/"+ent.Name+"/"+id.String()+"/detail-panel", nil,
		map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()})
	w := httptest.NewRecorder()
	s.detailPanelRecord(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("detailPanelRecord: код %d, тело %s", w.Code, w.Body.String())
	}
	var data detailPanelData
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatalf("ответ панели не разобрался: %v; raw=%s", err, w.Body.String())
	}
	return data
}

func TestInfoRegisterDetailPanel_UsesReferenceLabelAndPeriod(t *testing.T) {
	ir := &metadata.InfoRegister{
		Name: "Цены", Periodic: true,
		Dimensions: []metadata.Field{{Name: "Товар", Title: "Товар", RefEntity: "Товары"}},
		Resources:  []metadata.Field{{Name: "Цена", Title: "Цена", Type: metadata.FieldTypeNumber}},
	}
	rawUUID := "11111111-1111-1111-1111-111111111111"
	raw := infoRegisterDetailPanelJSON(ir, map[string]any{
		"period": "12.08.2026", "Товар": rawUUID, "Товар_label": "Кресло", "Цена": 18700,
	}, "ru")
	var data detailPanelData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	for label, want := range map[string]string{"Период": "12.08.2026", "Товар": "Кресло", "Цена": "18 700"} {
		if got, ok := detailPanelValueByLabel(data, label); !ok || got != want {
			t.Errorf("%s = %q, %v; ожидалось %q; payload=%+v", label, got, ok, want, data)
		}
	}
	if strings.Contains(raw, rawUUID) {
		t.Fatalf("панель показала UUID вместо подписи ссылки: %s", raw)
	}
}

func TestDetailPanel_HiddenFieldIsAbsentAndMaskedReferenceCannotUseStaleLabel(t *testing.T) {
	fields := []metadata.Field{
		{Name: "Скрыто", Title: "Скрыто", Type: metadata.FieldTypeString},
		{Name: "Контрагент", Title: "Контрагент", RefEntity: "Контрагенты"},
	}
	raw := detailPanelJSON(fields, map[string]any{
		// «Скрыто» отсутствует как после field_access.hide.
		"Контрагент": "••••••", "Контрагент_label": "Секретная подпись",
	}, "", nil, "ru")
	var data detailPanelData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	if _, ok := detailPanelValueByLabel(data, "Скрыто"); ok {
		t.Fatalf("hide-поле воскресло в payload: %+v", data)
	}
	if got, ok := detailPanelValueByLabel(data, "Контрагент"); !ok || got != "••••••" {
		t.Fatalf("маскированная ссылка использовала stale label: got=%q ok=%v payload=%+v", got, ok, data)
	}
}

// Payload собирается из уже отрисованной строки: значение, скрытое маской ПДн,
// в строку не попадает и в панели не появляется. Проверяем именно это свойство,
// а не «мы вызвали маску».
func TestDetailPanel_MaskedValueStaysMasked(t *testing.T) {
	ent := panelEntity()
	row := map[string]any{"Наименование": "Кресло", "Поставщик": "***"}
	raw := detailPanelJSON(ent.Fields, row, detailPanelTitle(ent.Fields, row), nil, "ru")
	if strings.Contains(raw, "ООО") {
		t.Errorf("в панель попало немаскированное значение: %s", raw)
	}
	if !strings.Contains(raw, "***") {
		t.Errorf("маскированное значение потерялось: %s", raw)
	}
}

// Клиентский runtime: панель следует за курсором, состояние в localStorage,
// ширина тянется. Без этого панель была бы статической картинкой.
func TestDetailPanel_ClientRuntime(t *testing.T) {
	js := string(uiJS)
	for _, want := range []string{
		"obDetailRender",      // отрисовка карточки
		"data-ob-detail",      // payload строки
		"localStorage",        // состояние
		"data-ob-detail-grip", // ручка ширины
		"obDetailToggle",      // кнопка включения
	} {
		if !strings.Contains(js, want) {
			t.Errorf("/static/ui.js не содержит %q", want)
		}
	}
	// Панель обязана перерисовываться при смене выбранной строки — иначе
	// стрелки двигают курсор, а карточка показывает прошлую запись.
	idx := strings.Index(js, "function listSetSel")
	if idx < 0 {
		t.Fatal("listSetSel не найдена")
	}
	if !strings.Contains(js[idx:idx+2000], "obDetailRender") {
		t.Error("listSetSel не перерисовывает панель")
	}
	if !strings.Contains(js, "tr.dataset.obDetailUrl = row.detail_url || ''") {
		t.Error("лениво загруженная строка дерева не получает защищённый detail URL")
	}
	if !strings.Contains(js, "if (typeof obDetailInvalidate === 'function') obDetailInvalidate()") {
		t.Error("live refresh не инвалидирует кэш панели деталей")
	}
	if !strings.Contains(js, "ob-detail-field-image") || !strings.Contains(js, "object-fit:contain") {
		t.Error("картинка панели не получает отдельную компоновку с автоподгоном")
	}
}

// Журнал теперь использует общий selection runtime. Старый journal-delegate
// открывал документ на single click и делал панель практически недоступной:
// generic click успевал выбрать строку, после чего браузер сразу уходил с неё.
func TestDetailPanel_JournalSingleClickDoesNotNavigate(t *testing.T) {
	js := string(uiJS)
	if strings.Contains(js, "data-ob-journal-open-url") {
		t.Fatal("в ui.js остался single-click навигатор журнала")
	}
	for _, want := range []string{
		"if (row) listRowClick(e, row)",
		"if (row) listRowDblClick(e, row)",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("общий click/dblclick контракт списка не содержит %q", want)
		}
	}
	if strings.Contains(tplJournal, "data-ob-journal-open-url") {
		t.Fatal("строка журнала всё ещё подключена к single-click навигатору")
	}
}

// Блок detail_panel задаёт состав и порядок закладок явно (план 118C).
func TestDetailPanel_ExplicitTabs(t *testing.T) {
	ent := panelEntity()
	ent.DetailPanel = &metadata.DetailPanel{
		Title: "Артикул",
		Tabs: []metadata.DetailPanelTab{
			{Name: "Цены", Fields: []string{"Поставщик"}},
			{Name: "Медиа", Fields: []string{"Фото", "Описание"}},
		},
	}
	row := map[string]any{
		"Наименование": "Кресло", "Артикул": "10041",
		"Поставщик": "ООО «Мебель»", "Фото": "img-1", "Описание": "<p>текст</p>",
	}
	var data detailPanelData
	raw := detailPanelForEntity(ent, row, nil, "ru")
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("payload: %v\n%s", err, raw)
	}
	if data.Title != "10041" {
		t.Errorf("заголовок из detail_panel.title = %q, ожидался артикул", data.Title)
	}
	if len(data.Tabs) != 2 || data.Tabs[0].Title != "Цены" || data.Tabs[1].Title != "Медиа" {
		t.Fatalf("закладки = %+v", data.Tabs)
	}
	// Внутри явной закладки типы не разносим: автор решил, что вместе.
	if len(data.Tabs[1].Fields) != 2 {
		t.Errorf("явная закладка разъехалась по типам: %+v", data.Tabs[1].Fields)
	}
	// Наименования нет ни в одной закладке — автор его не перечислил.
	if strings.Contains(raw, "Кресло") {
		t.Errorf("в панель попал реквизит вне объявленного состава: %s", raw)
	}
}

func TestDetailPanel_ExplicitTabPreservesMixedFieldOrderAndStableKeys(t *testing.T) {
	ent := panelEntity()
	ent.DetailPanel = &metadata.DetailPanel{Tabs: []metadata.DetailPanelTab{
		{Name: "Первая", Titles: map[string]string{"en": "Same"}, Fields: []string{"Фото", "Артикул", "Описание"}},
		{Name: "Вторая", Titles: map[string]string{"en": "Same"}, Fields: []string{"Поставщик"}},
	}}
	row := map[string]any{
		"Наименование": "Кресло", "Артикул": "10041", "Поставщик": "ООО",
		"Фото": "img-1", "Описание": "<p>text</p>",
	}
	var data detailPanelData
	if err := json.Unmarshal([]byte(detailPanelForEntity(ent, row, nil, "en")), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Tabs) != 2 || data.Tabs[0].Title != "Same" || data.Tabs[1].Title != "Same" {
		t.Fatalf("localized tabs = %+v", data.Tabs)
	}
	if data.Tabs[0].Key == data.Tabs[1].Key || data.Tabs[0].Key == "" || data.Tabs[1].Key == "" {
		t.Fatalf("translated titles are not backed by distinct stable keys: %+v", data.Tabs)
	}
	var labels []string
	for _, field := range data.Tabs[0].Fields {
		labels = append(labels, field.Label)
	}
	if got := strings.Join(labels, ","); got != "Фото,Артикул,Описание" {
		t.Fatalf("mixed field order changed: %s", got)
	}
	js := string(uiJS)
	for _, want := range []string{"function tabKey(tab)", "obDetailStore('tab', tabKey(tab))", "if (tabKey(tab) !== active)"} {
		if !strings.Contains(js, want) {
			t.Fatalf("client tab identity still depends on translated title: missing %q", want)
		}
	}
}

func TestDetailPanel_ConfiguredTitleUsesTypedFormatter(t *testing.T) {
	ent := panelEntity()
	ent.Fields = append(ent.Fields, metadata.Field{Name: "Статус", Type: metadata.FieldTypeString, EnumName: "Статусы"})
	ent.DetailPanel = &metadata.DetailPanel{Title: "Статус", Fields: []string{"Артикул"}, FieldsSet: true}
	raw := detailPanelForEntity(ent, map[string]any{"Наименование": "Кресло", "Артикул": "10041", "Статус": "active"},
		map[string]map[string]string{"Статус": {"active": "Активен"}}, "ru")
	var data detailPanelData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	if data.Title != "Активен" {
		t.Fatalf("enum title bypassed typed formatting: %q", data.Title)
	}
}

func TestDetailPanel_ManagedListPagesHavePriority(t *testing.T) {
	ent := panelEntity()
	ent.DetailPanel = &metadata.DetailPanel{Fields: []string{"Поставщик"}, FieldsSet: true}
	ent.Forms = []*metadata.FormModule{{
		Name: "ФормаСписка", Kind: "list", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementPages, Name: "Закладки",
			Children: []*metadata.FormElement{
				{Kind: metadata.FormElementPage, Name: "Карточка", TitleMap: map[string]string{"en": "Card"}, Children: []*metadata.FormElement{
					{Kind: metadata.FormElementField, DataPath: "Список.Артикул"},
					{Kind: metadata.FormElementPicture, DataPath: "Список.Фото"},
					{Kind: metadata.FormElementField, DataPath: "Товары.Поставщик"},
				}},
				{Kind: metadata.FormElementPage, Name: "Дополнительно", TitleMap: map[string]string{"en": "Extra"}, Children: []*metadata.FormElement{
					{Kind: metadata.FormElementField, DataPath: "Список.Описание"},
				}},
			},
		}},
	}}
	row := map[string]any{
		"Наименование": "Кресло", "Артикул": "10041", "Поставщик": "ООО",
		"Фото": "img-1", "Описание": "<p>text</p>",
	}
	var data detailPanelData
	if err := json.Unmarshal([]byte(detailPanelForEntity(ent, row, nil, "en")), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Tabs) != 2 || data.Tabs[0].Title != "Card" || data.Tabs[1].Title != "Extra" {
		t.Fatalf("managed pages were not projected in order: %+v", data.Tabs)
	}
	if got, ok := detailPanelValueByLabel(data, "Артикул"); !ok || got != "10041" {
		t.Fatalf("managed field missing: %+v", data)
	}
	if _, ok := detailPanelValueByLabel(data, "Поставщик"); ok {
		t.Fatalf("lower-priority detail_panel composition won over managed pages: %+v", data)
	}
	if data.Tabs[0].Fields[0].Label != "Артикул" || data.Tabs[0].Fields[1].Label != "Фото" {
		t.Fatalf("managed field order changed: %+v", data.Tabs[0].Fields)
	}
}

func TestDetailPanel_ManagedPagesDoNotFallbackPerRow(t *testing.T) {
	ent := panelEntity()
	ent.DetailPanel = &metadata.DetailPanel{Fields: []string{"Поставщик"}, FieldsSet: true}
	ent.Forms = []*metadata.FormModule{{
		Name: "ФормаСписка", Kind: "list", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementPages, Name: "Закладки",
			Children: []*metadata.FormElement{{
				Kind: metadata.FormElementPage, Name: "Скрытое",
				Children: []*metadata.FormElement{{Kind: metadata.FormElementField, DataPath: "Список.Артикул"}},
			}},
		}},
	}}
	// Артикул отсутствует exactly as after field_access.hide. The managed
	// composition is still authoritative for this row; Поставщик must not be
	// revealed by falling through to the lower-priority YAML source.
	row := map[string]any{"Наименование": "Кресло", "Поставщик": "ООО"}
	if got := detailPanelForEntity(ent, row, nil, "ru"); got != "" {
		t.Fatalf("managed page with a hidden field fell through to YAML/auto: %s", got)
	}
}

func TestDetailPanel_ManagedPagesFallbackOnlyWhenStructurallyEmpty(t *testing.T) {
	ent := panelEntity()
	ent.DetailPanel = &metadata.DetailPanel{Fields: []string{"Поставщик"}, FieldsSet: true}
	ent.Forms = []*metadata.FormModule{{
		Name: "ФормаСписка", Kind: "list", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementPages,
			Children: []*metadata.FormElement{{
				Kind: metadata.FormElementPage, Name: "ТЧ",
				Children: []*metadata.FormElement{{Kind: metadata.FormElementField, DataPath: "Товары.Поставщик"}},
			}},
		}},
	}}
	row := map[string]any{"Наименование": "Кресло", "Поставщик": "ООО"}
	var data detailPanelData
	if err := json.Unmarshal([]byte(detailPanelForEntity(ent, row, nil, "ru")), &data); err != nil {
		t.Fatal(err)
	}
	if got, ok := detailPanelValueByLabel(data, "Поставщик"); !ok || got != "ООО" {
		t.Fatalf("structurally empty managed pages blocked YAML fallback: %+v", data)
	}
}

func TestManagedDetailPanelTitleFallbackIsDeterministic(t *testing.T) {
	element := &metadata.FormElement{
		Name:     "StableName",
		TitleMap: map[string]string{"en": "English", "fr": "Français"},
	}
	if got := managedElementTitle(element, "de"); got != "StableName" {
		t.Fatalf("missing locale fell back to random map entry: %q", got)
	}
	element.Title = "Legacy"
	if got := managedElementTitle(element, "de"); got != "Legacy" {
		t.Fatalf("legacy title fallback = %q", got)
	}
	if got := managedElementTitle(element, "fr"); got != "Français" {
		t.Fatalf("exact locale title = %q", got)
	}
}

// Короткая форма detail_panel.fields — состав без закладок, разложенный по типам.
func TestDetailPanel_ExplicitFieldsShortForm(t *testing.T) {
	ent := panelEntity()
	ent.DetailPanel = &metadata.DetailPanel{Fields: []string{"Артикул", "Фото"}, FieldsSet: true}
	row := map[string]any{"Наименование": "Кресло", "Артикул": "10041", "Поставщик": "ООО", "Фото": "img-1"}
	var data detailPanelData
	if err := json.Unmarshal([]byte(detailPanelForEntity(ent, row, nil, "ru")), &data); err != nil {
		t.Fatalf("payload: %v", err)
	}
	titles := []string{}
	for _, tab := range data.Tabs {
		titles = append(titles, tab.Title)
	}
	if strings.Join(titles, ",") != "Основное,Изображения" {
		t.Errorf("закладки = %v", titles)
	}
	for _, tab := range data.Tabs {
		for _, f := range tab.Fields {
			if f.Label == "Поставщик" {
				t.Error("реквизит вне состава попал в панель")
			}
		}
	}
}

// Без блока — прежняя автокомпоновка: 118C не меняет поведение существующих
// конфигураций.
func TestDetailPanel_NoBlockKeepsAutoLayout(t *testing.T) {
	ent := panelEntity()
	row := map[string]any{"Наименование": "Кресло", "Артикул": "10041", "Фото": "img-1"}
	auto := detailPanelJSON(ent.Fields, row, detailPanelTitle(ent.Fields, row), nil, "ru")
	if got := detailPanelForEntity(ent, row, nil, "ru"); got != auto {
		t.Errorf("без блока состав изменился:\n%s\n%s", got, auto)
	}
}

func TestDetailPanel_ExplicitEmptyTabsFailsClosedAtRuntime(t *testing.T) {
	ent := panelEntity()
	ent.DetailPanel = &metadata.DetailPanel{TabsSet: true}
	row := map[string]any{"Наименование": "Кресло", "Артикул": "10041"}
	if got := detailPanelForEntity(ent, row, nil, "ru"); got != "" {
		t.Fatalf("explicit tabs: [] fell back to all fields: %s", got)
	}
}

// Ленивый хендлер — отдельный публичный вход, и права он обязан проверять сам,
// а не «наследовать» из списка, который его породил (#860).
func TestDetailPanel_HandlerПроверяетПраваСам(t *testing.T) {
	ent := &metadata.Entity{
		Name:   "Товар",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "Кресло"}, ent); err != nil {
		t.Fatal(err)
	}

	// Роль без права чтения этого справочника.
	user := &auth.User{ID: "no-read", Login: "no-read", Roles: []*auth.Role{{
		Name:        "Без доступа",
		Permissions: auth.Permission{Catalogs: map[string][]string{"Другой": {"read"}}},
	}}}
	r := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String()+"/detail-panel", nil,
		map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()})
	r = r.WithContext(auth.ContextWithUser(r.Context(), user))
	w := httptest.NewRecorder()
	s.detailPanelRecord(w, r)
	if w.Code == http.StatusOK {
		t.Fatalf("панель отдана без права чтения: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Кресло") {
		t.Fatalf("данные записи утекли в отказе: %s", w.Body.String())
	}
}

func TestDetailPanel_HandlerHonorsFormReadHook(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьДоступ()
	ВызватьИсключение("Нет доступа");
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventType("ПриЧтенииНаСервере"): "ПроверитьДоступ",
	}, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
	})
	id := insertContragent(t, srv, ent, "СЕКРЕТ-ДЕТАЛЬНОЙ-ПАНЕЛИ")
	target := "/ui/catalog/" + ent.Name + "/" + id.String() + "/detail-panel"
	r := reqWithChi(http.MethodGet, target, nil, map[string]string{
		"kind": "catalog", "entity": ent.Name, "id": id.String(),
	})
	w := httptest.NewRecorder()
	srv.detailPanelRecord(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("detail panel bypassed ПриЧтенииНаСервере: status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "СЕКРЕТ-ДЕТАЛЬНОЙ-ПАНЕЛИ") {
		t.Fatalf("detail panel leaked a row rejected by ПриЧтенииНаСервере: %s", w.Body.String())
	}
}

func TestDetailPanel_HandlerReportsSnapshotLoadFailureAsServerError(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьДоступ()
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventType("ПриЧтенииНаСервере"): "ПроверитьДоступ",
	}, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
	})
	id := insertContragent(t, srv, ent, "НЕ-ДОЛЖНО-УТЕЧЬ")
	// Табличная часть объявлена после миграции: чтение её отсутствующей таблицы
	// детерминированно имитирует storage failure при сборке snapshot для hook.
	ent.TableParts = []metadata.TablePart{{
		Name:   "Строки",
		Fields: []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}},
	}}
	target := "/ui/catalog/" + ent.Name + "/" + id.String() + "/detail-panel"
	r := reqWithChi(http.MethodGet, target, nil, map[string]string{
		"kind": "catalog", "entity": ent.Name, "id": id.String(),
	})
	w := httptest.NewRecorder()
	srv.detailPanelRecord(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("snapshot storage failure hidden as status %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "НЕ-ДОЛЖНО-УТЕЧЬ") {
		t.Fatalf("storage error response leaked record data: %s", w.Body.String())
	}
}

// RLS, ПриЧтенииНаСервере и payload обязаны использовать один снимок. Иначе
// конкурентное обновление между первым GetByID и внутренней загрузкой read-hook
// могло разрешить hook на новой строке, а в ответ вернуть старую секретную.
func TestDetailPanel_SnapshotBindsReadHookToReturnedRow(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьДоступ()
	Если Объект.Наименование = "СЕКРЕТНЫЙ-СНИМОК" Тогда
		ВызватьИсключение("Нет доступа");
	КонецЕсли;
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventType("ПриЧтенииНаСервере"): "ПроверитьДоступ",
	}, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
	})
	id := insertContragent(t, srv, ent, "СЕКРЕТНЫЙ-СНИМОК")
	form := pickObjectFormWithReadHook(ent)
	if form == nil {
		t.Fatal("read-hook form not found")
	}

	snapshot, err := srv.loadDetailPanelSnapshot(context.Background(), ent, form, id)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.rowAllowed || snapshot.hookObject == nil {
		t.Fatalf("incomplete authorized snapshot: %+v", snapshot)
	}

	// Имитируем конкурентное изменение ровно после получения снимка. Hook всё
	// равно должен увидеть секретное значение из snapshot, а не перечитать уже
	// разрешённую текущую строку из БД.
	if err := srv.store.Upsert(context.Background(), ent.Name, id,
		map[string]any{"Наименование": "ОТКРЫТЫЙ-СНИМОК"}, ent); err != nil {
		t.Fatal(err)
	}
	if err := srv.runFormReadHookOnObject(context.Background(), ent, form, snapshot.hookObject); err == nil {
		t.Fatal("read-hook перечитал новую строку вместо авторизации исходного снимка")
	}
	if got := snapshot.row["Наименование"]; got != "СЕКРЕТНЫЙ-СНИМОК" {
		t.Fatalf("returned snapshot changed after concurrent update: %v", got)
	}
}
