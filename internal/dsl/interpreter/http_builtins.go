package interpreter

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxDSLHTTPResponseBytes int64 = 16 << 20

// ─── dslHTTPConnection ────────────────────────────────────────────────────────

type dslHTTPConnection struct {
	host    string
	port    int
	https   bool
	timeout time.Duration
	guard   NetGuard
	ctx     CtxSource
}

func (c *dslHTTPConnection) CallMethod(name string, args []any) any {
	switch name {
	case "получить", "get":
		req := asHTTPRequest(args, 0, "HTTPСоединение.Получить")
		return c.do(req, "GET")
	case "отправитьдля", "sendfor":
		req := asHTTPRequest(args, 0, "HTTPСоединение.ОтправитьДля")
		method := "POST"
		if len(args) >= 2 {
			method = strings.ToUpper(fmt.Sprintf("%v", args[1]))
		}
		return c.do(req, method)
	}
	panic(userError{Msg: "HTTPСоединение: неизвестный метод " + name})
}

func (c *dslHTTPConnection) do(req *dslHTTPRequest, method string) *dslHTTPResponse {
	checkNet(c.guard)
	scheme := "http"
	if c.https {
		scheme = "https"
	}
	port := c.port
	var url string
	if port == 0 || (scheme == "http" && port == 80) || (scheme == "https" && port == 443) {
		url = fmt.Sprintf("%s://%s%s", scheme, c.host, req.resource)
	} else {
		url = fmt.Sprintf("%s://%s:%d%s", scheme, c.host, port, req.resource)
	}

	client := &http.Client{Timeout: c.timeout}
	var bodyReader io.Reader
	if req.body != "" {
		bodyReader = strings.NewReader(req.body)
	}
	ctx := contextFromSource(c.ctx)
	checkExecutionContext(ctx)
	httpReq, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		panic(userError{Msg: "HTTPСоединение: ошибка запроса: " + err.Error()})
	}
	for k, v := range req.headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		checkExecutionContext(ctx)
		panic(userError{Msg: "HTTPСоединение: " + err.Error()})
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
	bodyBytes := readDSLHTTPResponseContext(ctx, resp.Body, "HTTPСоединение")
	return &dslHTTPResponse{
		statusCode: resp.StatusCode,
		headers:    resp.Header,
		body:       string(bodyBytes),
	}
}

// ─── dslHTTPRequest ───────────────────────────────────────────────────────────

type dslHTTPRequest struct {
	resource string
	headers  map[string]string
	body     string
}

func (r *dslHTTPRequest) CallMethod(name string, args []any) any {
	switch name {
	case "установитьзаголовок", "setheader":
		if len(args) >= 2 {
			r.headers[fmt.Sprintf("%v", args[0])] = fmt.Sprintf("%v", args[1])
		}
		return nil
	case "установитьтелоизстроки", "setbodyfromstring":
		if len(args) > 0 {
			r.body = fmt.Sprintf("%v", args[0])
		}
		return nil
	}
	panic(userError{Msg: "HTTPЗапрос: неизвестный метод " + name})
}

// ─── dslHTTPResponse ──────────────────────────────────────────────────────────

type dslHTTPResponse struct {
	statusCode int
	headers    http.Header
	body       string
}

// Get implements This — allows Ответ.КодСостояния property access.
func (r *dslHTTPResponse) Get(field string) any {
	switch field {
	case "кодсостояния", "statuscode":
		return float64(r.statusCode)
	}
	return nil
}

func (r *dslHTTPResponse) Set(field string, val any) {}

func (r *dslHTTPResponse) CallMethod(name string, args []any) any {
	switch name {
	case "получитьтелокакстроку", "getbodyasstring":
		return r.body
	case "получитьзаголовок", "getheader":
		if len(args) > 0 {
			return r.headers.Get(fmt.Sprintf("%v", args[0]))
		}
		return ""
	}
	panic(userError{Msg: "HTTPОтвет: неизвестный метод " + name})
}

// NetGuard вызывается перед каждой сетевой операцией. Возвращает ошибку, если
// сеть заблокирована предохранителем (план 62). nil → без ограничений.
type NetGuard func() error

// checkNet паникует userError'ом, если guard запрещает сеть. Сообщение —
// человеческое: пользователь сразу понимает, где включить.
func checkNet(guard NetGuard) {
	if guard == nil {
		return
	}
	if err := guard(); err != nil {
		panic(userError{Msg: err.Error()})
	}
}

// ─── NewHTTPFunctions ─────────────────────────────────────────────────────────

// NewHTTPFunctions returns factories and shorthands to inject into DSL extraVars.
// guard (может быть nil) проверяется в момент сетевого вызова — предохранитель
// сети читается свежим, переключение в конфигураторе действует без перезапуска.
func NewHTTPFunctions(guard NetGuard, ctxSources ...CtxSource) map[string]any {
	ctxSource := firstCtxSource(ctxSources)
	m := map[string]any{
		"__factory_HTTPСоединение": newHTTPConnFactory(guard, ctxSource),
		"__factory_HTTPConnection": newHTTPConnFactory(guard, ctxSource),
		"__factory_HTTPЗапрос":     newHTTPReqFactory(),
		"__factory_HTTPRequest":    newHTTPReqFactory(),
	}

	httpGet := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		checkNet(guard)
		url := strArg(args, 0)
		ctx := contextFromSource(ctxSource)
		checkExecutionContext(ctx)
		client := &http.Client{Timeout: 30 * time.Second}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // G704: URL is the explicit DSL network capability, gated by NetGuard/sandbox policy
		if err != nil {
			panic(userError{Msg: "HTTPПолучить: " + err.Error()})
		}
		resp, err := client.Do(req) //nolint:gosec // G704: request is the explicit DSL network capability, gated by NetGuard/sandbox policy
		if err != nil {
			checkExecutionContext(ctx)
			panic(userError{Msg: "HTTPПолучить: " + err.Error()})
		}
		defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
		b := readDSLHTTPResponseContext(ctx, resp.Body, "HTTPПолучить")
		return &dslHTTPResponse{statusCode: resp.StatusCode, headers: resp.Header, body: string(b)}, nil
	})

	httpPost := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		checkNet(guard)
		url := strArg(args, 0)
		body := strArg(args, 1)
		contentType := "application/json"
		ctx := contextFromSource(ctxSource)
		checkExecutionContext(ctx)
		client := &http.Client{Timeout: 30 * time.Second}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body)) //nolint:gosec // G704: URL is the explicit DSL network capability, gated by NetGuard/sandbox policy
		if err != nil {
			panic(userError{Msg: "HTTPОтправить: " + err.Error()})
		}
		req.Header.Set("Content-Type", contentType)
		resp, err := client.Do(req) //nolint:gosec // G704: request is the explicit DSL network capability, gated by NetGuard/sandbox policy
		if err != nil {
			checkExecutionContext(ctx)
			panic(userError{Msg: "HTTPОтправить: " + err.Error()})
		}
		defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
		b := readDSLHTTPResponseContext(ctx, resp.Body, "HTTPОтправить")
		return &dslHTTPResponse{statusCode: resp.StatusCode, headers: resp.Header, body: string(b)}, nil
	})

	m["HTTPПолучить"] = httpGet
	m["HTTPGet"] = httpGet
	m["HTTPОтправить"] = httpPost
	m["HTTPPost"] = httpPost
	return m
}

func readDSLHTTPResponse(body io.Reader, caller string) []byte {
	return readDSLHTTPResponseContext(context.Background(), body, caller)
}

func readDSLHTTPResponseContext(ctx context.Context, body io.Reader, caller string) []byte {
	data, err := io.ReadAll(io.LimitReader(body, maxDSLHTTPResponseBytes+1))
	if err != nil {
		checkExecutionContext(ctx)
		panic(userError{Msg: caller + ": ошибка чтения ответа: " + err.Error()})
	}
	checkExecutionContext(ctx)
	if int64(len(data)) > maxDSLHTTPResponseBytes {
		panic(userError{Msg: fmt.Sprintf("%s: ответ превышает лимит %d MiB", caller, maxDSLHTTPResponseBytes>>20)})
	}
	return data
}

func newHTTPConnFactory(guard NetGuard, ctxSource CtxSource) func([]any) any {
	return func(args []any) any {
		conn := &dslHTTPConnection{
			host:    strArg(args, 0),
			timeout: 30 * time.Second,
			guard:   guard,
			ctx:     ctxSource,
		}
		if len(args) >= 2 {
			conn.port = int(floatArg(args, 1))
		}
		if len(args) >= 3 {
			conn.https = truthy(args[2])
		} else if conn.port == 443 {
			conn.https = true
		}
		if len(args) >= 4 {
			if secs := floatArg(args, 3); secs > 0 {
				conn.timeout = time.Duration(secs) * time.Second
			}
		}
		return conn
	}
}

func newHTTPReqFactory() func([]any) any {
	return func(args []any) any {
		return &dslHTTPRequest{
			resource: strArg(args, 0),
			headers:  make(map[string]string),
		}
	}
}

func asHTTPRequest(args []any, i int, caller string) *dslHTTPRequest {
	if i < len(args) {
		if req, ok := args[i].(*dslHTTPRequest); ok {
			return req
		}
	}
	panic(userError{Msg: caller + ": ожидается HTTPЗапрос"})
}
