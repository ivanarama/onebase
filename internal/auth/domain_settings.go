package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"
)

// DomainSettings describes one base's shared Kerberos endpoint. Only the
// server-side pathname of the keytab is stored, never its contents.
type DomainSettings struct {
	PublicURL string `json:"public_url"`
	SPN       string `json:"spn"`
	Realm     string `json:"realm"`
	Keytab    string `json:"keytab"`
}

const domainSettingsKey = "auth.domain_login"

var ErrInvalidDomainSettings = errors.New("неверные параметры доменного входа")

var dnsLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func validDomainName(name string) bool {
	if len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func (s DomainSettings) normalized() (DomainSettings, error) {
	s.PublicURL = strings.TrimSpace(s.PublicURL)
	s.SPN = strings.TrimSpace(s.SPN)
	s.Realm = strings.TrimSpace(s.Realm)
	s.Keytab = strings.TrimSpace(s.Keytab)
	if s == (DomainSettings{}) {
		return s, nil
	}
	u, err := url.Parse(s.PublicURL)
	if err != nil || u.Scheme != "https" || !validDomainName(u.Hostname()) || u.User != nil ||
		(u.EscapedPath() != "" && u.EscapedPath() != "/") || strings.ContainsAny(s.PublicURL, "?#") ||
		strings.HasSuffix(u.Host, ":") {
		return DomainSettings{}, fmt.Errorf("%w: укажите HTTPS-адрес базы без пути, параметров и учётных данных", ErrInvalidDomainSettings)
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
			return DomainSettings{}, fmt.Errorf("%w: недопустимый порт HTTPS", ErrInvalidDomainSettings)
		}
	}
	spnHost, ok := strings.CutPrefix(s.SPN, "HTTP/")
	if !ok || !strings.EqualFold(spnHost, u.Hostname()) {
		return DomainSettings{}, fmt.Errorf("%w: SPN должен иметь вид HTTP/имя-сайта и совпадать с именем в HTTPS-адресе", ErrInvalidDomainSettings)
	}
	// A Windows service uses SSPI without a keytab. When configuring a Linux
	// service from Windows, validate the path for the target operating system.
	if s.Realm != "" || s.Keytab != "" {
		if !validDomainName(s.Realm) {
			return DomainSettings{}, fmt.Errorf("%w: укажите домен Kerberos для сервера Linux", ErrInvalidDomainSettings)
		}
		if !path.IsAbs(s.Keytab) || path.Clean(s.Keytab) != s.Keytab || s.Keytab == "/" ||
			strings.IndexFunc(s.Keytab, unicode.IsControl) >= 0 {
			return DomainSettings{}, fmt.Errorf("%w: укажите абсолютный путь к файлу keytab на сервере Linux", ErrInvalidDomainSettings)
		}
	}
	s.PublicURL = strings.TrimSuffix(s.PublicURL, "/")
	return s, nil
}

func (r *Repo) StoredDomainSettings(ctx context.Context) (DomainSettings, error) {
	raw, ok, err := r.db.GetSetting(ctx, domainSettingsKey)
	if err != nil || !ok {
		return DomainSettings{}, err
	}
	var s *DomainSettings
	if err := json.Unmarshal([]byte(raw), &s); err != nil || s == nil {
		return DomainSettings{}, ErrInvalidDomainSettings
	}
	return s.normalized()
}

// EffectiveDomainSettings returns a complete profile from a single source.
// Existing deployments keep their process settings until a profile is saved.
func (r *Repo) EffectiveDomainSettings(ctx context.Context) (DomainSettings, error) {
	s, err := r.StoredDomainSettings(ctx)
	if err != nil || s != (DomainSettings{}) {
		return s, err
	}
	return DomainSettings{
		PublicURL: strings.TrimSpace(os.Getenv("ONEBASE_PUBLIC_URL")),
		SPN:       strings.TrimSpace(os.Getenv("ONEBASE_WINDOWS_SPN")),
		Realm:     strings.TrimSpace(os.Getenv("ONEBASE_KRB5_REALM")),
		Keytab:    os.Getenv("ONEBASE_KRB5_KEYTAB"),
	}, nil
}

func (r *Repo) SaveDomainSettings(ctx context.Context, s DomainSettings) error {
	s, err := s.normalized()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return r.db.SaveSetting(ctx, domainSettingsKey, string(raw))
}

func (s DomainSettings) accountsSupported() bool {
	return runtime.GOOS == "windows" || (runtime.GOOS == "linux" && s.Realm != "" && path.IsAbs(s.Keytab))
}

func (r *Repo) WindowsAccountsSupported(ctx context.Context) bool {
	if runtime.GOOS != "linux" {
		return runtime.GOOS == "windows"
	}
	s, err := r.EffectiveDomainSettings(ctx)
	return err == nil && s.accountsSupported()
}
