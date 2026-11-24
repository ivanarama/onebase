package auth

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

//go:embed kerberos_accept.py
var kerberosAcceptor string

// Each pre-authentication exchange starts a process with native libraries.
// Bound both simultaneous processes and their lifetime, including on disconnect.
var kerberosAcceptSlots = make(chan struct{}, 4)

func acceptWindowsTicket(ctx context.Context, spn string, ticket []byte) (string, []byte, error) {
	realm, keytab := os.Getenv("ONEBASE_KRB5_REALM"), os.Getenv("ONEBASE_KRB5_KEYTAB")
	if realm == "" || !filepath.IsAbs(keytab) || len(ticket) == 0 || len(ticket) > maxWindowsTicket {
		return "", nil, errors.New("Kerberos: отсутствует конфигурация или неверный размер билета")
	}
	select {
	case kerberosAcceptSlots <- struct{}{}:
		defer func() { <-kerberosAcceptSlots }()
	default:
		return "", nil, errors.New("Kerberos: сервер обрабатывает другие запросы входа")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// The system interpreter runs only embedded code in isolated mode. The
	// binary ticket is passed on stdin, never in a command line or a log.
	cmd := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-c", kerberosAcceptor, spn, realm, keytab) //nolint:gosec // G204: fixed executable and script; operator settings are separate arguments.
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(ticket)
	cmd.Stderr = io.Discard
	cmd.Env = []string{"LANG=C.UTF-8", "KRB5RCACHENAME=dfl:"}
	for _, key := range []string{"KRB5_CONFIG", "KRB5RCACHEDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	output, err := cmd.Output()
	if err != nil {
		var exited *exec.ExitError
		if errors.As(err, &exited) {
			switch exited.ExitCode() {
			case 2:
				return "", nil, errors.New("Kerberos: требуются пакеты python3-gssapi и python3-samba")
			case 3:
				return "", nil, errors.New("Kerberos: проверьте права на keytab и наличие в нём принципала службы")
			}
		}
		return "", nil, fmt.Errorf("Kerberos: проверка билета: %w", err)
	}
	sid, reply, ok := bytes.Cut(output, []byte{'\n'})
	if !ok || !validDomainUserSID(string(sid)) || len(reply) > maxWindowsTicket {
		return "", nil, errors.New("Kerberos: неверный ответ службы проверки билетов")
	}
	return string(sid), reply, nil
}
