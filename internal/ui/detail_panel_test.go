package ui

// Боковая панель деталей активной записи (план 118B, issue #670).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

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
	if !strings.Contains(html, "data-ob-detail='") {
		t.Error("в строке списка нет payload панели")
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
}
