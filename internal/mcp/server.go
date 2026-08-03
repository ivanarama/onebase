// Package mcp implements a small stdio Model Context Protocol server for
// OneBase developer tooling. It intentionally delegates work to the onebase CLI
// commands so MCP stays a thin transport wrapper over the tested tool surface.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const protocolVersion = "2025-06-18"

type Config struct {
	ID                  string
	Project             string
	SQLitePath          string
	DSN                 string
	AllowWrite          bool
	AllowFmtWrite       bool
	AllowRefactorWrite  bool
	AllowConfigRollback bool
	AllowProcrun        bool
	Timeout             time.Duration
}

type Server struct {
	cfg Config
	exe string
}

func New(cfg Config) (*Server, error) {
	if cfg.Project == "" {
		cfg.Project = "."
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, exe: exe}, nil
}

func (s *Server) Serve(in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(errorResponse(nil, -32700, "Parse error", err.Error()))
			continue
		}
		if len(req.ID) == 0 {
			s.handleNotification(req)
			continue
		}
		resp := s.handleRequest(req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (s *Server) handleNotification(req request) {
	// notifications/initialized currently carries no server-side state.
}

func (s *Server) handleRequest(req request) response {
	switch req.Method {
	case "initialize":
		return resultResponse(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"resources": map[string]any{},
				"tools":     map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "onebase",
				"title":   "OneBase Developer Tools",
				"version": "0.0.0",
			},
			"instructions": "Read-only OneBase developer tools. Mutating tools are unavailable unless onebase mcp is started with --allow-write or a specific --allow-*-write flag.",
		})
	case "ping":
		return resultResponse(req.ID, map[string]any{})
	case "tools/list":
		return resultResponse(req.ID, map[string]any{"tools": s.tools()})
	case "tools/call":
		res, err := s.callTool(req.Params)
		if err != nil {
			return resultResponse(req.ID, toolText(err.Error(), true))
		}
		return resultResponse(req.ID, res)
	case "resources/list":
		return resultResponse(req.ID, map[string]any{"resources": s.resources()})
	case "resources/templates/list":
		return resultResponse(req.ID, map[string]any{"resourceTemplates": []map[string]any{{
			"uriTemplate": "onebase://source/{path}",
			"name":        "source",
			"title":       "Project source file",
			"description": "Read a file under the configured OneBase project directory",
			"mimeType":    "text/plain",
		}}})
	case "resources/read":
		res, err := s.readResource(req.Params)
		if err != nil {
			return errorResponse(req.ID, -32602, err.Error(), nil)
		}
		return resultResponse(req.ID, res)
	default:
		return errorResponse(req.ID, -32601, "Method not found", req.Method)
	}
}

func resultResponse(id json.RawMessage, result any) response {
	return response{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id json.RawMessage, code int, message string, data any) response {
	return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message, Data: data}}
}

func toolText(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func (s *Server) tools() []map[string]any {
	tools := []map[string]any{
		tool("check", "Run onebase check for the project", objectSchema(nil, nil)),
		tool("config_versions", "List database-backed configuration snapshots", objectSchema(map[string]any{"limit": map[string]any{"type": "integer", "description": "Maximum versions"}}, nil)),
		tool("config_diff", "Diff two database-backed configuration snapshots", objectSchema(map[string]any{
			"before": stringProp("Before version id"),
			"after":  stringProp("After version id"),
		}, []string{"before", "after"})),
		tool("describe", "Return onebase describe output", objectSchema(map[string]any{"compact": boolProp("Return compact text instead of full JSON")}, nil)),
		tool("schema", "Return JSON Schema for OneBase metadata", objectSchema(map[string]any{"kind": stringProp("Schema kind, e.g. catalog, document, widget")}, nil)),
		tool("query", "Compile and run a read-only OneBase query", objectSchema(map[string]any{
			"query":  stringProp("OneBase query text, must start with ВЫБРАТЬ or SELECT"),
			"params": map[string]any{"type": "object", "description": "Query parameters"},
			"limit":  map[string]any{"type": "integer", "description": "Maximum rows"},
		}, []string{"query"})),
		tool("eval", "Evaluate a DSL expression or snippet in the restricted sandbox", objectSchema(map[string]any{
			"source":  stringProp("DSL expression or snippet"),
			"snippet": boolProp("Treat source as function body with explicit Возврат"),
		}, []string{"source"})),
		tool("examples", "Return canonical YAML/DSL examples", objectSchema(map[string]any{"kind": stringProp("Example kind")}, []string{"kind"})),
		tool("impact", "Find references to an object, field, or procedure", objectSchema(map[string]any{
			"object":    stringProp("Metadata object name"),
			"field":     stringProp("Field name"),
			"procedure": stringProp("Procedure/function name"),
		}, nil)),
		tool("refactor_preview", "Preview a safe refactor without writing files", objectSchema(map[string]any{
			"type":   stringProp("rename-object or rename-field"),
			"object": stringProp("Object name for rename-field"),
			"from":   stringProp("Old object/field name"),
			"to":     stringProp("New object/field name"),
		}, []string{"type", "from", "to"})),
		tool("widget_explain", "Explain a widget query/mapping and optional sample", objectSchema(map[string]any{
			"name":   stringProp("Widget name"),
			"sample": map[string]any{"type": "integer", "description": "Sample row count"},
		}, []string{"name"})),
		tool("report_explain", "Explain a report query/composition and optional sample", objectSchema(map[string]any{
			"name":   stringProp("Report name"),
			"sample": map[string]any{"type": "integer", "description": "Sample row count"},
			"params": map[string]any{"type": "object", "description": "Report/query params"},
		}, []string{"name"})),
		tool("fmt_check", "Check canonical YAML formatting without writing", objectSchema(map[string]any{"path": stringProp("Optional path under project")}, nil)),
	}
	if s.allowFmtWrite() {
		tools = append(tools, tool("fmt_write", "Format YAML files in place. Mutating tool; exposed with --allow-fmt-write or --allow-write.", objectSchema(map[string]any{"path": stringProp("Optional path under project")}, nil)))
	}
	if s.allowConfigRollback() {
		tools = append(tools, tool("config_rollback", "Rollback database-backed configuration to a snapshot. Mutating tool; exposed with --allow-config-rollback or --allow-write.", objectSchema(map[string]any{
			"version": stringProp("Version id to rollback to"),
			"message": stringProp("Optional rollback snapshot message"),
		}, []string{"version"})))
	}
	if s.allowRefactorWrite() {
		tools = append(tools, tool("refactor_write", "Apply a safe refactor and run onebase check. Mutating tool; exposed with --allow-refactor-write or --allow-write.", objectSchema(map[string]any{
			"type":   stringProp("rename-object or rename-field"),
			"object": stringProp("Object name for rename-field"),
			"from":   stringProp("Old object/field name"),
			"to":     stringProp("New object/field name"),
		}, []string{"type", "from", "to"})))
	}
	if s.allowProcrun() {
		tools = append(tools, tool("procrun", "Run a processor offline. Mutating; exposed with --allow-procrun or --allow-write.", objectSchema(map[string]any{
			"proc": stringProp("Processor name"),
			"set":  map[string]any{"type": "object", "description": "String parameters"},
		}, []string{"proc"})))
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i]["name"].(string) < tools[j]["name"].(string) })
	return tools
}

func (s *Server) allowFmtWrite() bool {
	return s.cfg.AllowWrite || s.cfg.AllowFmtWrite
}

func (s *Server) allowRefactorWrite() bool {
	return s.cfg.AllowWrite || s.cfg.AllowRefactorWrite
}

func (s *Server) allowConfigRollback() bool {
	return s.cfg.AllowWrite || s.cfg.AllowConfigRollback
}

func (s *Server) allowProcrun() bool {
	return s.cfg.AllowWrite || s.cfg.AllowProcrun
}

func tool(name, description string, schema map[string]any) map[string]any {
	return map[string]any{
		"name":        name,
		"title":       name,
		"description": description,
		"inputSchema": schema,
	}
}

func objectSchema(props map[string]any, required []string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	s := map[string]any{"type": "object", "properties": props}
	if required != nil {
		s["required"] = required
	}
	return s
}

func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func boolProp(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func (s *Server) callTool(raw json.RawMessage) (map[string]any, error) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	args, err := s.toolArgs(params.Name, params.Arguments)
	if err != nil {
		return nil, err
	}
	stdout, stderr, err := s.runCLI(args...)
	text := strings.TrimSpace(stdout)
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		if text != "" {
			text += "\n"
		}
		text += stderr
	}
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return toolText(text, true), nil
	}
	return toolText(text, false), nil
}

func (s *Server) toolArgs(name string, a map[string]any) ([]string, error) {
	switch name {
	case "check":
		return append([]string{"check"}, s.baseArgs(false)...), nil
	case "config_versions":
		args := append([]string{"config", "versions", "--json"}, s.baseArgs(true)...)
		if limit := intArg(a, "limit"); limit > 0 {
			args = append(args, "--limit", fmt.Sprint(limit))
		}
		return args, nil
	case "config_diff":
		before, after := stringArg(a, "before"), stringArg(a, "after")
		if before == "" || after == "" {
			return nil, fmt.Errorf("before and after are required")
		}
		return append([]string{"config", "diff", before, after, "--json"}, s.baseArgs(true)...), nil
	case "config_rollback":
		if !s.allowConfigRollback() {
			return nil, fmt.Errorf("config_rollback is disabled; start onebase mcp with --allow-config-rollback or --allow-write")
		}
		version := stringArg(a, "version")
		if version == "" {
			return nil, fmt.Errorf("version is required")
		}
		args := append([]string{"config", "rollback", version, "--json"}, s.baseArgs(true)...)
		if msg := stringArg(a, "message"); msg != "" {
			args = append(args, "--message", msg)
		}
		return args, nil
	case "describe":
		args := append([]string{"describe"}, s.baseArgs(false)...)
		if boolArg(a, "compact") {
			args = append(args, "--compact")
		}
		return args, nil
	case "schema":
		args := []string{"schema"}
		if kind := stringArg(a, "kind"); kind != "" {
			args = append(args, kind)
		}
		return args, nil
	case "query":
		q := stringArg(a, "query")
		if q == "" {
			return nil, fmt.Errorf("query is required")
		}
		args := append([]string{"query", q, "--json"}, s.baseArgs(true)...)
		if params, ok := a["params"]; ok {
			b, _ := json.Marshal(params)
			args = append(args, "--params", string(b))
		}
		if limit := intArg(a, "limit"); limit > 0 {
			args = append(args, "--limit", fmt.Sprint(limit))
		}
		return args, nil
	case "eval":
		src := stringArg(a, "source")
		if src == "" {
			return nil, fmt.Errorf("source is required")
		}
		args := append([]string{"eval", src, "--json"}, s.baseArgs(true)...)
		if boolArg(a, "snippet") {
			args = append(args, "--snippet")
		}
		return args, nil
	case "examples":
		kind := stringArg(a, "kind")
		if kind == "" {
			return nil, fmt.Errorf("kind is required")
		}
		return []string{"examples", kind}, nil
	case "impact":
		args := append([]string{"impact", "--json"}, s.baseArgs(false)...)
		for _, k := range []string{"object", "field", "procedure"} {
			if v := stringArg(a, k); v != "" {
				args = append(args, "--"+k, v)
			}
		}
		return args, nil
	case "refactor_preview", "refactor_write":
		write := name == "refactor_write"
		if write && !s.allowRefactorWrite() {
			return nil, fmt.Errorf("refactor_write is disabled; start onebase mcp with --allow-refactor-write or --allow-write")
		}
		typ := stringArg(a, "type")
		if typ != "rename-object" && typ != "rename-field" {
			return nil, fmt.Errorf("type must be rename-object or rename-field")
		}
		from, to := stringArg(a, "from"), stringArg(a, "to")
		if from == "" || to == "" {
			return nil, fmt.Errorf("from and to are required")
		}
		args := append([]string{"refactor", typ, "--from", from, "--to", to, "--json"}, s.baseArgs(false)...)
		if typ == "rename-field" {
			object := stringArg(a, "object")
			if object == "" {
				return nil, fmt.Errorf("object is required for rename-field")
			}
			args = append(args, "--object", object)
		}
		if write {
			args = append(args, "--write")
		}
		return args, nil
	case "widget_explain":
		name := stringArg(a, "name")
		if name == "" {
			return nil, fmt.Errorf("name is required")
		}
		args := append([]string{"widget", "explain", name, "--json"}, s.baseArgs(true)...)
		if sample := intArg(a, "sample"); sample > 0 {
			args = append(args, "--sample", fmt.Sprint(sample))
		}
		return args, nil
	case "report_explain":
		name := stringArg(a, "name")
		if name == "" {
			return nil, fmt.Errorf("name is required")
		}
		args := append([]string{"report", "explain", name, "--json"}, s.baseArgs(true)...)
		if sample := intArg(a, "sample"); sample > 0 {
			args = append(args, "--sample", fmt.Sprint(sample))
		}
		if params, ok := a["params"]; ok {
			b, _ := json.Marshal(params)
			args = append(args, "--params", string(b))
		}
		return args, nil
	case "fmt_check":
		args := []string{"fmt", "--check"}
		if path := stringArg(a, "path"); path != "" {
			p, err := s.safeProjectPath(path)
			if err != nil {
				return nil, err
			}
			args = append(args, p)
		} else {
			args = append(args, "--project", s.cfg.Project)
		}
		return args, nil
	case "fmt_write":
		if !s.allowFmtWrite() {
			return nil, fmt.Errorf("fmt_write is disabled; start onebase mcp with --allow-fmt-write or --allow-write")
		}
		args := []string{"fmt"}
		if path := stringArg(a, "path"); path != "" {
			p, err := s.safeProjectPath(path)
			if err != nil {
				return nil, err
			}
			args = append(args, p)
		} else {
			args = append(args, "--project", s.cfg.Project)
		}
		return args, nil
	case "procrun":
		if !s.allowProcrun() {
			return nil, fmt.Errorf("procrun is disabled; start onebase mcp with --allow-procrun or --allow-write")
		}
		proc := stringArg(a, "proc")
		if proc == "" {
			return nil, fmt.Errorf("proc is required")
		}
		args := append([]string{"procrun", "--proc", proc}, s.baseArgs(true)...)
		if set, ok := a["set"].(map[string]any); ok {
			for k, v := range set {
				args = append(args, "--set", k+"="+fmt.Sprint(v))
			}
		}
		return args, nil
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *Server) baseArgs(withDB bool) []string {
	if s.cfg.ID != "" {
		return []string{"--id", s.cfg.ID}
	}
	args := []string{"--project", s.cfg.Project}
	if withDB {
		if s.cfg.SQLitePath != "" {
			args = append(args, "--sqlite", s.cfg.SQLitePath)
		}
		if s.cfg.DSN != "" {
			args = append(args, "--db", s.cfg.DSN)
		}
	}
	return args
}

func (s *Server) runCLI(args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.exe, append([]string{"--no-gui"}, args...)...) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("command timed out after %s", s.cfg.Timeout)
	}
	return stdout.String(), stderr.String(), err
}

func (s *Server) resources() []map[string]any {
	return []map[string]any{
		resource("onebase://ai-guide", "ai-guide", "Generated OneBase AI developer guide", "text/markdown"),
		resource("onebase://describe/full", "describe-full", "Full describe JSON", "application/json"),
		resource("onebase://describe/compact", "describe-compact", "Compact AI context", "text/plain"),
		resource("onebase://schema/all", "schema-all", "All OneBase JSON Schemas", "application/schema+json"),
		resource("onebase://source-tree", "source-tree", "Project source tree", "text/plain"),
	}
}

func resource(uri, name, description, mime string) map[string]any {
	return map[string]any{"uri": uri, "name": name, "title": name, "description": description, "mimeType": mime}
}

func (s *Server) readResource(raw json.RawMessage) (map[string]any, error) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	uri := params.URI
	var text, mime string
	switch uri {
	case "onebase://ai-guide":
		stdout, stderr, err := s.runCLI("ai-guide")
		if err != nil {
			return nil, errors.New(strings.TrimSpace(stdout + "\n" + stderr))
		}
		text, mime = stdout, "text/markdown"
	case "onebase://describe/full":
		stdout, stderr, err := s.runCLI(append([]string{"describe"}, s.baseArgs(false)...)...)
		if err != nil {
			return nil, errors.New(strings.TrimSpace(stdout + "\n" + stderr))
		}
		text, mime = stdout, "application/json"
	case "onebase://describe/compact":
		stdout, stderr, err := s.runCLI(append([]string{"describe", "--compact"}, s.baseArgs(false)...)...)
		if err != nil {
			return nil, errors.New(strings.TrimSpace(stdout + "\n" + stderr))
		}
		text, mime = stdout, "text/plain"
	case "onebase://schema/all":
		stdout, stderr, err := s.runCLI("schema")
		if err != nil {
			return nil, errors.New(strings.TrimSpace(stdout + "\n" + stderr))
		}
		text, mime = stdout, "application/schema+json"
	case "onebase://source-tree":
		tree, err := s.sourceTree()
		if err != nil {
			return nil, err
		}
		text, mime = tree, "text/plain"
	default:
		if strings.HasPrefix(uri, "onebase://source/") {
			path := strings.TrimPrefix(uri, "onebase://source/")
			decoded, err := url.PathUnescape(path)
			if err != nil {
				return nil, err
			}
			full, err := s.safeProjectPath(decoded)
			if err != nil {
				return nil, err
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return nil, err
			}
			text, mime = string(data), "text/plain"
		} else {
			return nil, fmt.Errorf("unknown resource uri: %s", uri)
		}
	}
	return map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": mime, "text": text}}}, nil
}

func (s *Server) sourceTree() (string, error) {
	root, err := filepath.Abs(s.cfg.Project)
	if err != nil {
		return "", err
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".hg", ".svn", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		low := strings.ToLower(rel)
		if strings.HasSuffix(low, ".yaml") || strings.HasSuffix(low, ".yml") || strings.HasSuffix(low, ".os") || strings.HasSuffix(low, ".md") {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	return strings.Join(files, "\n"), nil
}

func (s *Server) safeProjectPath(path string) (string, error) {
	if s.cfg.ID != "" {
		return "", fmt.Errorf("source file resources require --project, not --id")
	}
	root, err := filepath.Abs(s.cfg.Project)
	if err != nil {
		return "", err
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, path)
	}
	full, err = filepath.Abs(full)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root: %s", path)
	}
	return full, nil
}

func stringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func boolArg(args map[string]any, key string) bool {
	if args == nil {
		return false
	}
	v, _ := args[key].(bool)
	return v
}

func intArg(args map[string]any, key string) int {
	if args == nil {
		return 0
	}
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}
