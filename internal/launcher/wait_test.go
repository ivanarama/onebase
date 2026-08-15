package launcher

import (
	"testing"
	"time"
)

func TestWaitForCloseReturnsWhenDoneCloses(t *testing.T) {
	done := make(chan struct{})
	close(done)
	returned := make(chan struct{})
	go func() {
		WaitForClose(done)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("WaitForClose did not return for a closed done channel")
	}
}
