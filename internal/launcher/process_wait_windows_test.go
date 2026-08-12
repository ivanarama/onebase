//go:build windows

package launcher

import (
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestCheckedWindowsProcessID(t *testing.T) {
	for _, pid := range []int{-1, 0} {
		if _, err := checkedWindowsProcessID(pid); err == nil {
			t.Fatalf("checkedWindowsProcessID(%d) unexpectedly succeeded", pid)
		}
	}
	if got, err := checkedWindowsProcessID(1); err != nil || got != 1 {
		t.Fatalf("checkedWindowsProcessID(1) = (%d, %v), want (1, nil)", got, err)
	}
	if strconv.IntSize == 64 {
		tooLarge := int(int64(^uint32(0)) + 1)
		if _, err := checkedWindowsProcessID(tooLarge); err == nil {
			t.Fatalf("checkedWindowsProcessID(%d) unexpectedly accepted a non-DWORD PID", tooLarge)
		}
	}
}

func TestCheckedWindowsWaitMillis(t *testing.T) {
	maxFinite := uint32(windows.INFINITE - 1)
	maxDuration := time.Duration(maxFinite) * time.Millisecond
	tests := []struct {
		name    string
		timeout time.Duration
		want    uint32
	}{
		{name: "negative", timeout: -time.Second, want: 0},
		{name: "poll", timeout: 0, want: 0},
		{name: "positive sub-millisecond", timeout: time.Nanosecond, want: 1},
		{name: "whole millisecond", timeout: time.Millisecond, want: 1},
		{name: "round up", timeout: 1500 * time.Microsecond, want: 2},
		{name: "largest finite", timeout: maxDuration, want: maxFinite},
		{name: "clamp", timeout: maxDuration + time.Second, want: maxFinite},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkedWindowsWaitMillis(tt.timeout); got != tt.want {
				t.Fatalf("checkedWindowsWaitMillis(%s) = %d, want %d", tt.timeout, got, tt.want)
			}
		})
	}
}
