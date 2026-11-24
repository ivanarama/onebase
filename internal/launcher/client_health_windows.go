package launcher

import (
	"context"
	"net/url"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winHTTP              = windows.NewLazySystemDLL("winhttp.dll")
	winHTTPOpen          = winHTTP.NewProc("WinHttpOpen")
	winHTTPConnect       = winHTTP.NewProc("WinHttpConnect")
	winHTTPOpenRequest   = winHTTP.NewProc("WinHttpOpenRequest")
	winHTTPSetTimeouts   = winHTTP.NewProc("WinHttpSetTimeouts")
	winHTTPSendRequest   = winHTTP.NewProc("WinHttpSendRequest")
	winHTTPReceive       = winHTTP.NewProc("WinHttpReceiveResponse")
	winHTTPQueryHeaders  = winHTTP.NewProc("WinHttpQueryHeaders")
	winHTTPClose         = winHTTP.NewProc("WinHttpCloseHandle")
	winHealthConcurrency = make(chan struct{}, 4)
)

type winHealthResult struct {
	status int
	err    error
}

// WinHTTP automatic proxy uses the logged-in user's proxy and PAC. Calls are
// synchronous, so bound the number of outstanding native requests when a PAC
// lookup outlives the caller's deadline.
func systemClientHealth(ctx context.Context, target *url.URL) (int, error, bool) {
	select {
	case winHealthConcurrency <- struct{}{}:
	case <-ctx.Done():
		return 0, ctx.Err(), true
	}
	done := make(chan winHealthResult, 1)
	go func() {
		defer func() { <-winHealthConcurrency }()
		deadline, _ := ctx.Deadline()
		ms := time.Until(deadline).Milliseconds()
		if ms < 1 {
			done <- winHealthResult{err: context.DeadlineExceeded}
			return
		}
		status, err := winHealth(target, uintptr(ms))
		done <- winHealthResult{status, err}
	}()
	select {
	case res := <-done:
		return res.status, res.err, true
	case <-ctx.Done():
		return 0, ctx.Err(), true
	}
}

func winHealth(target *url.URL, timeout uintptr) (int, error) {
	const (
		automaticProxy  = 4
		flagSecure      = 0x00800000
		queryStatusCode = 19 | 0x20000000
	)
	agent, verb := windows.StringToUTF16Ptr("OneBase"), windows.StringToUTF16Ptr("GET")
	host, err := windows.UTF16PtrFromString(target.Hostname())
	if err != nil {
		return 0, probeBadAddress
	}
	resource, err := windows.UTF16PtrFromString(target.RequestURI())
	if err != nil {
		return 0, probeBadAddress
	}
	port, flags := uint64(80), uintptr(0)
	if target.Scheme == "https" {
		port, flags = 443, flagSecure
	}
	if target.Port() != "" {
		port, err = strconv.ParseUint(target.Port(), 10, 16)
		if err != nil || port == 0 {
			return 0, probeBadAddress
		}
	}
	session, _, callErr := winHTTPOpen.Call(uintptr(unsafe.Pointer(agent)), automaticProxy, 0, 0, 0)
	if session == 0 {
		return 0, winHealthError(callErr)
	}
	defer winHTTPClose.Call(session)
	if ok, _, e := winHTTPSetTimeouts.Call(session, timeout, timeout, timeout, timeout); ok == 0 {
		return 0, winHealthError(e)
	}
	conn, _, callErr := winHTTPConnect.Call(session, uintptr(unsafe.Pointer(host)), uintptr(port), 0)
	if conn == 0 {
		return 0, winHealthError(callErr)
	}
	defer winHTTPClose.Call(conn)
	request, _, callErr := winHTTPOpenRequest.Call(conn, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(resource)), 0, 0, 0, flags)
	if request == 0 {
		return 0, winHealthError(callErr)
	}
	defer winHTTPClose.Call(request)
	if ok, _, e := winHTTPSendRequest.Call(request, 0, 0, 0, 0, 0, 0); ok == 0 {
		return 0, winHealthError(e)
	}
	if ok, _, e := winHTTPReceive.Call(request, 0); ok == 0 {
		return 0, winHealthError(e)
	}
	var status uint32
	length := uint32(unsafe.Sizeof(status))
	if ok, _, e := winHTTPQueryHeaders.Call(request, queryStatusCode, 0, uintptr(unsafe.Pointer(&status)), uintptr(unsafe.Pointer(&length)), 0); ok == 0 {
		return 0, winHealthError(e)
	}
	return int(status), nil
}

func winHealthError(err error) clientProbeIssue {
	switch err {
	case syscall.Errno(12002): // WINHTTP_TIMEOUT
		return probeTimeout
	case syscall.Errno(12037), syscall.Errno(12038), syscall.Errno(12045), syscall.Errno(12057),
		syscall.Errno(12169), syscall.Errno(12170), syscall.Errno(12179):
		return probeCert
	case syscall.Errno(12157), syscall.Errno(12175):
		return probeTLS
	case syscall.Errno(12166), syscall.Errno(12167), syscall.Errno(12178), syscall.Errno(12180):
		return probeProxy
	default:
		return probeNetwork
	}
}
