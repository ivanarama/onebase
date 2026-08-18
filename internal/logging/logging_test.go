package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactURI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/ui?_tk=secret123", "/ui?_tk=***"},
		{"/auth/bootstrap?code=abc123&return=/ui", "/auth/bootstrap?code=***&return=/ui"},
		{"/x?token=tok&y=2", "/x?token=***&y=2"},
		{"/x?api_key=k1&apikey=k2", "/x?api_key=***&apikey=***"},
		{"/x?password=p", "/x?password=***"},
		{"/x?TOKEN=tok", "/x?TOKEN=***"},
		{"/plain", "/plain"},
		{"/q?a=1&b=2", "/q?a=1&b=2"},
		{"/q?", "/q?"},
		// Capability-токен /pub — само право доступа: в лог только префикс.
		{"/pub/AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJ", "/pub/AAAABBBB***"},
		{"/pub/AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJ?dl=1", "/pub/AAAABBBB***?dl=1"},
		{"/pub/short", "/pub/short"},
		{"/hs/site/pub/page", "/hs/site/pub/page"},
	}
	for _, c := range cases {
		if got := RedactURI(c.in); got != c.want {
			t.Errorf("RedactURI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLoggerRedactsSensitiveAttrs(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, true, slog.LevelDebug)

	logger.Info("login",
		"token", "secret-token",
		"request_uri", "/ui?_tk=secret123&x=1",
		"auth", slog.GroupValue(slog.String("password", "p1"), slog.String("user", "ivan")),
	)

	out := buf.String()
	for _, leaked := range []string{"secret-token", "secret123", `"password":"p1"`} {
		if strings.Contains(out, leaked) {
			t.Fatalf("log leaked %q: %s", leaked, out)
		}
	}
	for _, want := range []string{`"token":"***"`, `"/ui?_tk=***&x=1"`, `"password":"***"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("log does not contain redacted marker %q: %s", want, out)
		}
	}
}

func TestRedactArgs(t *testing.T) {
	got := RedactArgs([]string{
		"run", "--db", "postgres://u:p@host/db", "--token=abc", "--project", "examples/minimal", "-password", "p1",
	})
	want := []string{"run", "--db", "***", "--token=***", "--project", "examples/minimal", "-password", "***"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("RedactArgs = %#v, want %#v", got, want)
	}
}
