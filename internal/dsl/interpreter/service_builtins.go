package interpreter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Объекты HTTP-сервисов (серверная сторона) — аналог 1С «HTTPСервисЗапрос» и
// «HTTPСервисОтвет». Запрос строит роутер (internal/ui) и передаёт его в
// обработчик как параметр; ответ обработчик создаёт сам через
// `Новый HTTPСервисОтвет(…)` либо короткими функциями ОтветJSON/ОтветТекст.
//
// Пример обработчика (src/<имя>.service.os):
//
//	Функция Получить(Запрос) Экспорт
//	    ид = Запрос.ПараметрыURL.Получить("id");
//	    Ответ = Новый HTTPСервисОтвет(200);
//	    Ответ.УстановитьТелоJSON(Новый Структура("id", ид));
//	    Возврат Ответ;
//	КонецФункции

// ─── DSLServiceRequest ────────────────────────────────────────────────────────

// DSLServiceRequest — входящий запрос к HTTP-сервису. Экспортирован, чтобы
// роутер мог его сконструировать (NewServiceRequest). Реализует This (доступ к
// свойствам Метод/ПараметрыURL/…) и MethodCallable (ПолучитьТелоКакСтроку и т.п.).
type DSLServiceRequest struct {
	method     string
	rootURL    string
	path       string
	pathParams map[string]string
	query      url.Values
	headers    http.Header
	body       []byte

	// Предсобранные коллекции для свойств (строятся один раз в конструкторе,
	// чтобы повторный доступ Запрос.ПараметрыURL возвращал стабильный объект).
	pathMap   *Map
	queryMap  *Map
	headerMap *Map
}

// NewServiceRequest собирает объект запроса для передачи в DSL-обработчик.
func NewServiceRequest(method, rootURL, path string, pathParams map[string]string, query url.Values, headers http.Header, body []byte) *DSLServiceRequest {
	r := &DSLServiceRequest{
		method:     strings.ToUpper(method),
		rootURL:    rootURL,
		path:       path,
		pathParams: pathParams,
		query:      query,
		headers:    headers,
		body:       body,
	}
	r.pathMap = mapFromStrings(pathParams)
	r.queryMap = mapFromValues(query)
	r.headerMap = mapFromHeader(headers)
	return r
}

func (r *DSLServiceRequest) Get(field string) any {
	switch strings.ToLower(field) {
	case "метод", "method":
		return r.method
	case "базовыйurl", "baseurl":
		return "/" + r.rootURL
	case "адресресурса", "resourceaddress", "путь", "path":
		return r.path
	case "параметрыurl", "urlparameters":
		return r.pathMap
	case "параметрызапроса", "queryparameters":
		return r.queryMap
	case "заголовки", "headers":
		return r.headerMap
	}
	return nil
}

func (r *DSLServiceRequest) Set(field string, val any) {} // запрос только для чтения

func (r *DSLServiceRequest) CallMethod(name string, args []any) any {
	switch name {
	case "получитьтелокакстроку", "getbodyasstring":
		return string(r.body)
	case "получитьзаголовок", "getheader":
		if len(args) > 0 {
			return r.headers.Get(fmt.Sprintf("%v", args[0]))
		}
		return ""
	case "параметрurl", "urlparameter":
		if len(args) > 0 {
			return r.pathParams[fmt.Sprintf("%v", args[0])]
		}
		return ""
	case "параметрзапроса", "queryparameter":
		if len(args) > 0 {
			return r.query.Get(fmt.Sprintf("%v", args[0]))
		}
		return ""
	case "формаданные", "formdata":
		// Разбор обычной HTML-формы (application/x-www-form-urlencoded).
		// Без этого приём формы в конфигурации невозможен в принципе: значения
		// приходят в процентном кодировании, а декодера в DSL нет — кириллица и
		// пробелы превращались бы в мусор.
		//
		// Возвращается Соответствие имя → значение; для повторяющихся имён
		// берётся первое: множественный выбор — отдельная задача, и молча
		// склеивать значения в строку хуже, чем не поддерживать их вовсе.
		values, err := url.ParseQuery(string(r.body))
		if err != nil {
			panic(userError{Msg: "HTTPСервисЗапрос.ФормаДанные: " + err.Error()})
		}
		out := &Map{}
		for key, vals := range values {
			if len(vals) == 0 {
				continue
			}
			out.CallMethod("вставить", []any{key, vals[0]})
		}
		return out
	case "телоjson", "bodyjson", "прочитатьтелокакjson":
		// Удобство сверх 1С: сразу разобрать тело как JSON в Структуру/Массив.
		var raw any
		if err := json.Unmarshal(r.body, &raw); err != nil {
			panic(userError{Msg: "HTTPСервисЗапрос.ТелоJSON: " + err.Error()})
		}
		return jsonToValue(raw)
	}
	panic(userError{Msg: "HTTPСервисЗапрос: неизвестный метод " + name})
}

// ─── DSLServiceResponse ───────────────────────────────────────────────────────

// DSLServiceResponse — ответ HTTP-сервиса. Создаётся обработчиком через
// `Новый HTTPСервисОтвет(КодСостояния[, ТипСодержимого])` и возвращается через
// Возврат. Роутер читает результат экспортированными методами.
type DSLServiceResponse struct {
	statusCode int
	headers    *Map // живая Соответствие-коллекция заголовков (Ответ.Заголовки.Вставить(...))
	body       []byte
}

func newServiceResponse(args []any) any {
	code := 200
	if len(args) >= 1 {
		if f, ok := toFloat(args[0]); ok && f != 0 {
			code = int(f)
		}
	}
	r := &DSLServiceResponse{statusCode: code, headers: &Map{}}
	if len(args) >= 2 {
		if ct := fmt.Sprintf("%v", args[1]); ct != "" {
			r.headers.CallMethod("вставить", []any{"Content-Type", ct})
		}
	}
	return r
}

func (r *DSLServiceResponse) Get(field string) any {
	switch strings.ToLower(field) {
	case "кодсостояния", "statuscode":
		return float64(r.statusCode)
	case "заголовки", "headers":
		return r.headers
	}
	return nil
}

func (r *DSLServiceResponse) Set(field string, val any) {
	switch strings.ToLower(field) {
	case "кодсостояния", "statuscode":
		if f, ok := toFloat(val); ok {
			r.statusCode = int(f)
		}
	}
}

func (r *DSLServiceResponse) CallMethod(name string, args []any) any {
	switch name {
	case "установитьтелоизстроки", "setbodyfromstring":
		if len(args) > 0 {
			r.body = []byte(fmt.Sprintf("%v", args[0]))
		}
		return nil
	case "установитьтелоиздвоичныхданных", "setbodyfrombinarydata":
		if len(args) > 0 {
			r.body = toBytes(args[0])
		}
		return nil
	case "установитьтелоjson", "setbodyjson":
		if len(args) > 0 {
			data, err := json.Marshal(valueToJSON(args[0]))
			if err != nil {
				panic(userError{Msg: "HTTPСервисОтвет.УстановитьТелоJSON: " + err.Error()})
			}
			r.body = data
			if r.headers.findIdx("Content-Type") < 0 {
				r.headers.CallMethod("вставить", []any{"Content-Type", "application/json; charset=utf-8"})
			}
		}
		return nil
	case "установитьзаголовок", "setheader":
		if len(args) >= 2 {
			r.headers.CallMethod("вставить", []any{fmt.Sprintf("%v", args[0]), fmt.Sprintf("%v", args[1])})
		}
		return nil
	case "установитькодсостояния", "setstatuscode":
		if len(args) > 0 {
			if f, ok := toFloat(args[0]); ok {
				r.statusCode = int(f)
			}
		}
		return nil
	}
	panic(userError{Msg: "HTTPСервисОтвет: неизвестный метод " + name})
}

// StatusCodeValue, BodyBytes, HeadersMap — экспортированные аксессоры для роутера.
func (r *DSLServiceResponse) StatusCodeValue() int { return r.statusCode }
func (r *DSLServiceResponse) BodyBytes() []byte    { return r.body }

// HeadersMap разворачивает Соответствие-заголовки в обычную карту строк.
func (r *DSLServiceResponse) HeadersMap() map[string]string {
	out := make(map[string]string, len(r.headers.keys))
	for i, k := range r.headers.keys {
		out[fmt.Sprintf("%v", k)] = fmt.Sprintf("%v", r.headers.vals[i])
	}
	return out
}

// ─── Конструкторы и короткие функции ──────────────────────────────────────────

// NewServiceFunctions возвращает фабрики и short-hand'ы для инжекта в DSL.
func NewServiceFunctions() map[string]any {
	m := map[string]any{
		"__factory_HTTPСервисОтвет":     newServiceResponse,
		"__factory_HTTPServiceResponse": newServiceResponse,
	}

	// ОтветJSON(КодСостояния, Значение) → готовый ответ с JSON-телом.
	respJSON := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		code := 200
		if len(args) >= 1 {
			if f, ok := toFloat(args[0]); ok && f != 0 {
				code = int(f)
			}
		}
		resp := newServiceResponse([]any{float64(code)}).(*DSLServiceResponse)
		if len(args) >= 2 {
			resp.CallMethod("установитьтелоjson", []any{args[1]})
		}
		return resp, nil
	})

	// ОтветТекст(КодСостояния, Текст[, ТипСодержимого]) → ответ с текстовым телом.
	respText := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		code := 200
		if len(args) >= 1 {
			if f, ok := toFloat(args[0]); ok && f != 0 {
				code = int(f)
			}
		}
		ctorArgs := []any{float64(code)}
		if len(args) >= 3 {
			ctorArgs = append(ctorArgs, args[2])
		}
		resp := newServiceResponse(ctorArgs).(*DSLServiceResponse)
		if len(args) >= 2 {
			resp.CallMethod("установитьтелоизстроки", []any{args[1]})
		}
		return resp, nil
	})

	m["ОтветJSON"] = respJSON
	m["ResponseJSON"] = respJSON
	m["ОтветТекст"] = respText
	m["ResponseText"] = respText
	return m
}

// MarshalDSLValue сериализует произвольное DSL-значение (Структура/Соответствие/
// Массив/число/строка/…) в JSON. Используется роутером, когда обработчик вернул
// «голое» значение вместо HTTPСервисОтвет — тогда оно отдаётся как JSON 200.
func MarshalDSLValue(v any) ([]byte, error) {
	return json.Marshal(valueToJSON(v))
}

// ─── вспомогательные построители коллекций ───────────────────────────────────

func mapFromStrings(src map[string]string) *Map {
	m := &Map{}
	for k, v := range src {
		m.CallMethod("вставить", []any{k, v})
	}
	return m
}

func mapFromValues(src url.Values) *Map {
	m := &Map{}
	for k, vs := range src {
		if len(vs) > 0 {
			m.CallMethod("вставить", []any{k, vs[0]})
		}
	}
	return m
}

func mapFromHeader(src http.Header) *Map {
	m := &Map{}
	for k := range src {
		m.CallMethod("вставить", []any{k, src.Get(k)})
	}
	return m
}

// toBytes приводит DSL-значение к []byte (строка или уже []byte).
func toBytes(v any) []byte {
	switch x := v.(type) {
	case []byte:
		return x
	case string:
		return []byte(x)
	default:
		return []byte(fmt.Sprintf("%v", x))
	}
}
