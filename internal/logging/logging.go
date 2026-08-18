// Package logging centralizes structured logging and redaction of sensitive
// fields. It keeps production logs machine-readable without leaking tokens,
// passwords, cookies, DSNs or one-time codes.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

var sensitiveKeys = map[string]bool{
	"_tk":           true,
	"api_key":       true,
	"apikey":        true,
	"authorization": true,
	"code":          true,
	"cookie":        true,
	"db":            true,
	"dsn":           true,
	"password":      true,
	"secret":        true,
	"session":       true,
	"token":         true,
}

// ConfigureDefault installs the process-wide slog default logger. Text format
// is the local default; JSON is enabled with ONEBASE_LOG_FORMAT=json or
// ONEBASE_ENV=production.
func ConfigureDefault() {
	level := new(slog.LevelVar)
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ONEBASE_LOG_LEVEL"))) {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn", "warning":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}

	format := strings.ToLower(strings.TrimSpace(os.Getenv("ONEBASE_LOG_FORMAT")))
	env := strings.ToLower(strings.TrimSpace(os.Getenv("ONEBASE_ENV")))
	json := format == "json" || (format == "" && env == "production")
	slog.SetDefault(New(os.Stderr, json, level))
}

// New returns a redacting slog logger writing either text or JSON records.
func New(w io.Writer, json bool, level slog.Leveler) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if json {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(redactingHandler{h: h})
}

// Component returns the default logger annotated with a stable component name.
func Component(name string) *slog.Logger {
	return slog.Default().With("component", name)
}

// RedactURI masks sensitive query parameter values while preserving the rest of
// the URI as-is, including raw escaping and parameter order.
func RedactURI(uri string) string {
	uri = redactPubToken(uri)
	q := strings.IndexByte(uri, '?')
	if q < 0 {
		return uri
	}
	path, query := uri[:q], uri[q+1:]
	parts := strings.Split(query, "&")
	changed := false
	for idx, p := range parts {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			continue
		}
		if IsSensitiveKey(p[:eq]) {
			parts[idx] = p[:eq] + "=***"
			changed = true
		}
	}
	if !changed {
		return uri
	}
	return path + "?" + strings.Join(parts, "&")
}

// redactPubToken прячет capability-токен публичной ссылки /pub/<токен> (план
// 127): знание токена и есть право доступа к файлу, поэтому целиком в лог он
// попадать не должен. Короткий префикс остаётся для корреляции записей.
func redactPubToken(uri string) string {
	const marker = "/pub/"
	if !strings.HasPrefix(uri, marker) {
		return uri
	}
	rest := uri[len(marker):]
	end := len(rest)
	for j := 0; j < len(rest); j++ {
		if rest[j] == '/' || rest[j] == '?' {
			end = j
			break
		}
	}
	if end <= 8 {
		return uri
	}
	return marker + rest[:8] + "***" + rest[end:]
}

// RedactArgs masks values of sensitive CLI flags while preserving argument
// count/order for diagnostics.
func RedactArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		arg := out[i]
		if strings.HasPrefix(arg, "--") {
			nameValue := strings.TrimPrefix(arg, "--")
			if eq := strings.IndexByte(nameValue, '='); eq >= 0 {
				if IsSensitiveKey(nameValue[:eq]) {
					out[i] = "--" + nameValue[:eq] + "=***"
				}
				continue
			}
			if IsSensitiveKey(nameValue) && i+1 < len(out) {
				out[i+1] = "***"
			}
			continue
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			name := strings.TrimLeft(arg, "-")
			if IsSensitiveKey(name) && i+1 < len(out) {
				out[i+1] = "***"
			}
		}
	}
	return out
}

// IsSensitiveKey reports whether a log key must be masked.
func IsSensitiveKey(key string) bool {
	return sensitiveKeys[normalizeKey(key)]
}

func normalizeKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.Trim(key, "\"'")
	key = strings.ReplaceAll(key, "-", "_")
	return key
}

type redactingHandler struct {
	h slog.Handler
}

func (h redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.h.Enabled(ctx, level)
}

func (h redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.h.Handle(ctx, out)
}

func (h redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return redactingHandler{h: h.h.WithAttrs(redactAttrs(attrs))}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{h: h.h.WithGroup(name)}
}

func redactAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactAttr(a)
	}
	return out
}

func redactAttr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	if IsSensitiveKey(a.Key) {
		a.Value = slog.StringValue("***")
		return a
	}
	if a.Value.Kind() == slog.KindGroup {
		a.Value = slog.GroupValue(redactAttrs(a.Value.Group())...)
		return a
	}
	if a.Value.Kind() == slog.KindString {
		switch normalizeKey(a.Key) {
		case "uri", "url", "request_uri":
			a.Value = slog.StringValue(RedactURI(a.Value.String()))
		}
	}
	return a
}
