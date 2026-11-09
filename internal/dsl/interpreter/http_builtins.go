package interpreter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxDSLHTTPResponseBytes int64 = 16 << 20

// DefaultDSLHTTPFileBytes — предел тела ответа, которое HTTPСоединение пишет
// в выходной файл. Совпадает с пределом загрузки вложений по умолчанию:
// скачанный файл обычно тут же прикрепляется (ПрисоединитьФайл), и больший
// файл всё равно не прошёл бы в хранилище. Хост задаёт свой предел через
// HTTPOptions.MaxFileBytes.
const DefaultDSLHTTPFileBytes int64 = 50 << 20

// ─── dslHTTPConnection ────────────────────────────────────────────────────────

type dslHTTPConnection struct {
	host    string
	port    int
	https   bool
	timeout time.Duration
	guard   NetGuard
	ctx     CtxSource
	// fileGuard проверяется перед записью ответа в выходной файл: файловая
	// возможность не должна открываться через сетевую. Песочница с запретом
	// файлов ставит его на фабрике (applySandboxVars).
	fileGuard    FileGuard
	maxFileBytes int64
}

// Получить(Запрос[, ИмяВыходногоФайла]) и ОтправитьДля(Запрос[, Метод[,
// ИмяВыходногоФайла]]): непустое ИмяВыходногоФайла — как в 1С, тело ответа
// пишется в файл побайтно и в строку не читается.
func (c *dslHTTPConnection) CallMethod(name string, args []any) any {
	switch name {
	case "получить", "get":
		req := asHTTPRequest(args, 0, "HTTPСоединение.Получить")
		return c.do(req, "GET", strArg(args, 1), "HTTPСоединение.Получить")
	case "отправитьдля", "sendfor":
		req := asHTTPRequest(args, 0, "HTTPСоединение.ОтправитьДля")
		method := "POST"
		if len(args) >= 2 {
			method = strings.ToUpper(fmt.Sprintf("%v", args[1]))
		}
		return c.do(req, method, strArg(args, 2), "HTTPСоединение.ОтправитьДля")
	}
	panic(userError{Msg: "HTTPСоединение: неизвестный метод " + name})
}

func (c *dslHTTPConnection) do(req *dslHTTPRequest, method, outFile, caller string) *dslHTTPResponse {
	checkNet(c.guard)
	// Запрет файлов и путь проверяются ДО запроса: иначе ответ скачивался бы
	// впустую, а песочница узнавала бы о записи только после сетевого вызова.
	var outPath string
	if outFile != "" {
		checkFile(c.fileGuard)
		outPath = safePathOrRaise(caller, outFile)
	}
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
	if outPath != "" {
		writeDSLHTTPResponseFile(ctx, resp.Body, outPath, c.fileLimit(), caller)
		return &dslHTTPResponse{
			statusCode: resp.StatusCode,
			headers:    resp.Header,
			bodyFile:   outPath,
		}
	}
	bodyBytes := readDSLHTTPResponseContext(ctx, resp.Body, "HTTPСоединение")
	return &dslHTTPResponse{
		statusCode: resp.StatusCode,
		headers:    resp.Header,
		body:       string(bodyBytes),
	}
}

func (c *dslHTTPConnection) fileLimit() int64 {
	if c.maxFileBytes > 0 {
		return c.maxFileBytes
	}
	return DefaultDSLHTTPFileBytes
}

// writeDSLHTTPResponseFile пишет тело во временный файл рядом с целевым и
// переименовывает его только после успешного чтения всего тела в пределах
// лимита. Любая ошибка (обрыв, дедлайн, превышение) удаляет временный файл:
// по пути ИмяВыходногоФайла не остаётся обрезанного содержимого.
func writeDSLHTTPResponseFile(ctx context.Context, body io.Reader, path string, limit int64, caller string) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.part") //nolint:gosec // G703: путь прошёл safePathOrRaise, как у остальных файловых builtins
	if err != nil {
		panic(userError{Msg: caller + ": " + err.Error()})
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	n, err := io.Copy(tmp, io.LimitReader(body, limit+1))
	if err != nil {
		checkExecutionContext(ctx)
		panic(userError{Msg: caller + ": ошибка чтения ответа: " + err.Error()})
	}
	checkExecutionContext(ctx)
	if n > limit {
		panic(userError{Msg: fmt.Sprintf("%s: ответ превышает лимит %d байт", caller, limit)})
	}
	// Close пишущей стороны проверяем: он сбрасывает буфер, и без проверки
	// файл мог бы остаться усечённым при сообщении об успехе.
	if err := tmp.Close(); err != nil {
		panic(userError{Msg: caller + ": " + err.Error()})
	}
	if err := os.Rename(tmpName, path); err != nil { //nolint:gosec // G703: путь прошёл safePathOrRaise, как у остальных файловых builtins
		panic(userError{Msg: caller + ": " + err.Error()})
	}
	done = true
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
	// bodyFile — путь, куда записано тело (ИмяВыходногоФайла); тогда body пуст.
	bodyFile string
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
		if r.bodyFile != "" {
			// Как в 1С: тело ушло в файл, строкового тела у ответа нет.
			return nil
		}
		return r.body
	case "получитьимяфайлатела", "getbodyfilename":
		return r.bodyFile
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

// HTTPOptions — необязательные настройки HTTP-клиента DSL.
type HTTPOptions struct {
	// Ctx — источник контекста исполнения (дедлайн и отмена запроса).
	Ctx CtxSource
	// MaxFileBytes — предел тела, записываемого в ИмяВыходногоФайла;
	// 0 → DefaultDSLHTTPFileBytes.
	MaxFileBytes int64
}

// NewHTTPFunctions returns factories and shorthands to inject into DSL extraVars.
// guard (может быть nil) проверяется в момент сетевого вызова — предохранитель
// сети читается свежим, переключение в конфигураторе действует без перезапуска.
func NewHTTPFunctions(guard NetGuard, ctxSources ...CtxSource) map[string]any {
	return NewHTTPFunctionsWith(guard, HTTPOptions{Ctx: firstCtxSource(ctxSources)})
}

// NewHTTPFunctionsWith — NewHTTPFunctions с явными настройками.
func NewHTTPFunctionsWith(guard NetGuard, opts HTTPOptions) map[string]any {
	ctxSource := opts.Ctx
	m := map[string]any{
		"__factory_HTTPСоединение": newHTTPConnFactory(guard, ctxSource, opts.MaxFileBytes),
		"__factory_HTTPConnection": newHTTPConnFactory(guard, ctxSource, opts.MaxFileBytes),
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

	// HTTPАдресРазрешён is a side-effect-free preflight for URLs sourced from
	// untrusted files. HTTPПолучитьБезопасно repeats the same policy and adds
	// DNS/IP validation at the actual dial, so the preflight is never the
	// security boundary by itself.
	httpURLAllowed := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		return safeHTTPURLAllowed(strArg(args, 0), strArg(args, 1)), nil
	})
	httpSafeGet := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		rawURL, rawPolicy := strArg(args, 0), strArg(args, 1)
		policy, parsedURL, matched, policyErr := matchSafeHTTPURL(rawURL, rawPolicy)
		if !matched {
			return nil, nil
		}
		if policyErr != nil {
			panic(userError{Msg: "HTTPПолучитьБезопасно: " + policyErr.Error()})
		}
		checkNet(guard)
		ctx := contextFromSource(ctxSource)
		checkExecutionContext(ctx)
		resp, err := performSafeHTTPGet(ctx, policy, parsedURL)
		if err != nil {
			checkExecutionContext(ctx)
			panic(userError{Msg: "HTTPПолучитьБезопасно: " + err.Error()})
		}
		return resp, nil
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
	m["HTTPАдресРазрешён"] = httpURLAllowed
	m["HTTPURLAllowed"] = httpURLAllowed
	m["HTTPПолучитьБезопасно"] = httpSafeGet
	m["HTTPSafeGet"] = httpSafeGet
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

func newHTTPConnFactory(guard NetGuard, ctxSource CtxSource, maxFileBytes int64) func([]any) any {
	return func(args []any) any {
		conn := &dslHTTPConnection{
			host:         strArg(args, 0),
			timeout:      30 * time.Second,
			guard:        guard,
			ctx:          ctxSource,
			maxFileBytes: maxFileBytes,
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

// denyHTTPFileOutput оборачивает фабрику HTTPСоединение так, что созданное
// соединение отказывается писать ответ в файл. Сетевой guard и остальные
// настройки исходной фабрики сохраняются.
func denyHTTPFileOutput(factory func([]any) any, deny FileGuard) func([]any) any {
	return func(args []any) any {
		v := factory(args)
		if conn, ok := v.(*dslHTTPConnection); ok {
			conn.fileGuard = deny
		}
		return v
	}
}

var errHTTPFileDenied = errors.New("файловые операции запрещены в этом режиме (песочница)")

func asHTTPRequest(args []any, i int, caller string) *dslHTTPRequest {
	if i < len(args) {
		if req, ok := args[i].(*dslHTTPRequest); ok {
			return req
		}
	}
	panic(userError{Msg: caller + ": ожидается HTTPЗапрос"})
}
