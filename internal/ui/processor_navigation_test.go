package ui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/stretchr/testify/require"
)

func TestProcessorNavigationPrefillsFormHTTP(t *testing.T) {
	sourceForm := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Открыть",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Открыть"},
	})
	sourceForm.ProgramAST = mustParse(t, `
Процедура Открыть()
	П = Новый Соответствие;
	П.Вставить("Начало", "2026-09-01");
	П.Вставить("Отбор", "ОТК & партнёры");
	П.Вставить("Включен", Ложь);
	П.Вставить("Защищён", "другое значение из URL");
	П.Вставить("tp_json.Направления", "[{""Имя"":""Север""}]");
	ОтправитьУведомление("*", "ui.открытьФорму",
		Новый Структура("вид,сущность,параметры,сформировать", "processor", "Итоги", П, Истина));
КонецПроцедуры`)
	src := &processor.Processor{Name: "Финотчёт", Forms: []*metadata.FormModule{sourceForm}}
	targetForm := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "Дата", Type: "date", DataPath: "Объект.Начало"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "Отбор", DataPath: "Объект.Отбор"},
		&metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "Включен", DataPath: "Объект.Включен"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "Защищён", DataPath: "Объект.Защищён", ReadOnly: true},
		&metadata.FormElement{
			Kind: metadata.FormElementTablePart, Name: "Направления", DataPath: "Объект.Направления",
			Children: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Имя", DataPath: "Имя"}},
		},
	)
	params := []processor.Param{
		{Name: "Начало", Type: "date", Default: "2001-01-01"},
		{Name: "Отбор", Type: "string", Default: "все"},
		{Name: "Включен", Type: "bool", Default: true},
		{Name: "Защищён", Type: "string", Default: "из настроек"},
	}
	dst := &processor.Processor{
		Name: "Итоги", Params: params, Forms: []*metadata.FormModule{targetForm},
		TableParts: []metadata.TablePart{{Name: "Направления", Fields: []metadata.Field{{Name: "Имя", Type: metadata.FieldTypeString}}}},
	}
	legacy := &processor.Processor{Name: "ИтогиБезМакета", Params: params}
	s, _ := newProcessorFormEventExecutionServer(t, src, nil)
	s.reg.LoadProcessors([]*processor.Processor{src, dst, legacy})
	s.hub = realtime.NewHub()
	mux := chi.NewRouter()
	s.Mount(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/ui/events", nil)
	require.NoError(t, err)
	stream, err := server.Client().Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, stream.Body.Close()) }()
	require.Equal(t, http.StatusOK, stream.StatusCode)
	waitSubscriber(t, s.hub)

	sourceURL := "/ui/processor/" + url.PathEscape(src.Name)
	page := processorNavigationGET(t, server, sourceURL)
	require.Contains(t, page, `data-ob-fire-click="Открыть"`)
	click := processorClickBody("Открыть")
	resp, err := server.Client().PostForm(server.URL+sourceURL+"/form-event", click)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	result := decodeFormEventResponse(t, body)
	require.True(t, result.OK, result.Error)

	frame := readDataFrame(t, stream.Body)
	target := runFormURLNode(t, "console.log(formURL(("+frame+").data));")
	uri, err := url.Parse(target)
	require.NoError(t, err)
	require.False(t, uri.Query().Has("__run"))
	form := processorNavigationGET(t, server, target)
	require.Regexp(t, `name="Начало"[^>]+value="2026-09-01T00:00:00"`, form)
	require.Regexp(t, `name="Отбор"[^>]+value="ОТК &amp; партнёры"`, form)
	require.Regexp(t, `name="Включен"[^>]+>`, form)
	require.NotRegexp(t, `name="Включен"[^>]+checked`, form)
	require.Contains(t, form, "из настроек")
	require.NotContains(t, form, "другое значение из URL")
	require.Contains(t, form, "Север")

	form = processorNavigationGET(t, server, "/ui/processor/"+url.PathEscape(legacy.Name)+"?"+uri.RawQuery)
	require.Regexp(t, `name="Начало"[^>]+value="2026-09-01"`, form)
	require.NotRegexp(t, `name="Включен"[^>]+checked`, form)
	form = processorNavigationGET(t, server, "/ui/processor/"+url.PathEscape(dst.Name))
	require.Regexp(t, `name="Начало"[^>]+value="2001-01-01"`, form)
	require.Regexp(t, `name="Включен"[^>]+checked`, form)
}

func processorNavigationGET(t *testing.T, server *httptest.Server, path string) string {
	t.Helper()
	resp, err := server.Client().Get(server.URL + path)
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return strings.TrimSpace(string(body))
}
