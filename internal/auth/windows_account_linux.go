package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func (r *Repo) resolveWindowsAccount(ctx context.Context, name string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	settings, err := r.EffectiveDomainSettings(ctx)
	if err != nil {
		return "", "", err
	}
	return resolveWinbindAccount(name, settings.Realm, func(option string) (string, error) {
		// Each option is constructed below. The account is a single option value,
		// including when it contains spaces or starts with a dash.
		cmd := exec.CommandContext(ctx, "/usr/bin/wbinfo", option) //nolint:gosec // G204: fixed binary, no shell.
		cmd.Env = append(os.Environ(), "LC_ALL=C.UTF-8")
		output, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("не удалось определить учётную запись через winbind (wbinfo): %w", err)
		}
		return strings.TrimSuffix(string(output), "\n"), nil
	})
}

func resolveWinbindAccount(name, realm string, wbinfo func(string) (string, error)) (string, string, error) {
	domain, user, ok := strings.Cut(name, `\`)
	// wbinfo parses both parts into Samba fstrings (256 bytes including NUL).
	// Reject long names before wbinfo can silently look up a truncated account.
	if !ok || domain == "" || user == "" || len(domain) >= 256 || len(user) >= 256 ||
		strings.Contains(user, `\`) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", "", errors.New(`укажите учётную запись в формате ДОМЕН\пользователь`)
	}
	if realm == "" {
		return "", "", errors.New("укажите домен Kerberos в параметрах базы или ONEBASE_KRB5_REALM")
	}
	separator, err := wbinfo("--separator")
	if err != nil {
		return "", "", err
	}
	if len(separator) != 1 || strings.Contains(domain, separator) {
		return "", "", errors.New("недопустимый разделитель домена winbind")
	}
	info, err := wbinfo("--domain-info=" + domain)
	if err != nil {
		return "", "", err
	}
	fields := map[string]string{}
	for line := range strings.SplitSeq(info, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	shortDomain := fields["Name"]
	dnsDomain := fields["Alt_Name"]
	if fields["Active Directory"] != "Yes" || !strings.EqualFold(dnsDomain, realm) {
		return "", "", errors.New("для привязки требуется учётная запись настроенного домена Active Directory")
	}
	if shortDomain == "" || len(shortDomain) >= 256 || strings.Contains(shortDomain, separator) ||
		(!strings.EqualFold(domain, shortDomain) && !strings.EqualFold(domain, dnsDomain)) {
		return "", "", errors.New("winbind вернул другой домен для указанной учётной записи")
	}
	lookup, err := wbinfo("--name-to-sid=" + shortDomain + separator + user)
	if err != nil {
		return "", "", err
	}
	// Samba prints "<sid> SID_USER (1)". A workstation's local SAM and AD
	// both issue S-1-5-21 SIDs, so also require the queried AD domain's SID.
	result := strings.Fields(lookup)
	if len(result) != 3 || result[2] != "(1)" || !validDomainUserSID(result[0]) {
		return "", "", errors.New("для входа требуется учётная запись пользователя домена")
	}
	if result[0][:strings.LastIndexByte(result[0], '-')] != fields["SID"] {
		return "", "", errors.New("SID учётной записи не принадлежит указанному домену")
	}
	return result[0], shortDomain + `\` + user, nil
}

// validDomainUserSID checks the canonical AD account SID, including its RID.
func validDomainUserSID(sid string) bool {
	tail, ok := strings.CutPrefix(sid, "S-1-5-21-")
	parts := strings.Split(tail, "-")
	if !ok || len(parts) != 4 || parts[3] == "0" {
		return false
	}
	for _, part := range parts {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil || strconv.FormatUint(n, 10) != part {
			return false
		}
	}
	return true
}
