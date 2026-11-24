package launcher

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
)

const clientServerProbeTimeout = 10 * time.Second

type clientProbeIssue string

const (
	probeBadAddress clientProbeIssue = "Некорректный адрес сервера"
	probeTimeout    clientProbeIssue = "Истекло время проверки сервера"
	probeProxy      clientProbeIssue = "Ошибка подключения через прокси"
	probeProxyAuth  clientProbeIssue = "Прокси требует авторизацию"
	probeTLS        clientProbeIssue = "Ошибка TLS или сертификата сервера"
	probeCert       clientProbeIssue = "Сертификат сервера не прошёл проверку"
	probeNetwork    clientProbeIssue = "Не удалось подключиться к серверу"
	probeHTTP       clientProbeIssue = "Сервер вернул ошибку при проверке"
)

func (p clientProbeIssue) Error() string { return string(p) }

func classifyClientProbeError(err error) clientProbeIssue {
	var known clientProbeIssue
	if errors.As(err, &known) {
		return known
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return probeTimeout
	}
	var verify *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &verify) || errors.As(err, &authority) || errors.As(err, &host) || errors.As(err, &invalid) {
		return probeCert
	}
	var requestError *url.Error
	if errors.As(err, &requestError) {
		err = requestError.Err
	}
	var header tls.RecordHeaderError
	if errors.As(err, &header) || strings.HasPrefix(err.Error(), "tls:") {
		return probeTLS
	}
	if strings.Contains(err.Error(), "Proxy Authentication Required") {
		return probeProxyAuth
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "proxyconnect" {
		return probeProxy
	}
	return probeNetwork
}

// Keep explicit Go proxy settings and bypasses, including loopback. A
// synthetic proxy lets httpproxy evaluate NO_PROXY even without HTTPS_PROXY.
func healthUsesEnvironment(target *url.URL, config httpproxy.Config) bool {
	if target.Scheme == "https" && config.HTTPSProxy != "" ||
		target.Scheme == "http" && config.HTTPProxy != "" {
		return true
	}
	config.HTTPProxy, config.HTTPSProxy = "http://proxy.invalid", "http://proxy.invalid"
	proxy, err := config.ProxyFunc()(target)
	return err == nil && proxy == nil
}

func probeClientServer(ctx context.Context, base *Base, client *http.Client) clientProbeIssue {
	if base == nil || !base.Client() {
		return probeBadAddress
	}
	address, err := normalizeServerURL(base.ServerURL)
	if err != nil {
		return probeBadAddress
	}
	target, err := url.Parse(address + "/health")
	if err != nil || target.Hostname() == "" {
		return probeBadAddress
	}
	preferSystem := client == nil && !healthUsesEnvironment(target, *httpproxy.FromEnvironment())
	if client == nil {
		client = http.DefaultClient
	}
	var issue clientProbeIssue
	for range 2 {
		attempt, cancel := context.WithTimeout(ctx, clientServerProbeTimeout)
		status, err := clientHealthRequest(attempt, target, client, preferSystem)
		cancel()
		switch {
		case err != nil:
			issue = classifyClientProbeError(err)
		case status == http.StatusProxyAuthRequired:
			issue = probeProxyAuth
		case status >= 200 && status < 500:
			return ""
		default:
			issue = probeHTTP
		}
		if ctx.Err() != nil {
			break
		}
	}
	return issue
}

func clientHealthRequest(ctx context.Context, target *url.URL, client *http.Client, preferSystem bool) (int, error) {
	if preferSystem {
		if status, err, supported := systemClientHealth(ctx, target); supported {
			return status, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return 0, probeBadAddress
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}
