package interpreter

import (
	"io"
	"strings"
	"testing"
)

func TestReadDSLHTTPResponseRejectsOversizedBody(t *testing.T) {
	defer func() {
		recovered := recover()
		err, ok := recovered.(userError)
		if !ok {
			t.Fatalf("panic = %#v, want userError", recovered)
		}
		if !strings.Contains(err.Msg, "16 MiB") {
			t.Fatalf("message = %q", err.Msg)
		}
	}()
	readDSLHTTPResponse(io.LimitReader(zeroReader{}, maxDSLHTTPResponseBytes+1), "HTTPПолучить")
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
