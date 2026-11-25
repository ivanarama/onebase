package auth

import (
	"errors"
	"testing"
)

func TestDomainSettingsSelectsOneProfilePerBase(t *testing.T) {
	repo, db, ctx := newCacheTestRepo(t)
	other, _, _ := newCacheTestRepo(t)
	service := DomainSettings{"https://old.example.org", "HTTP/old.example.org", "EXAMPLE.ORG", "/etc/onebase/old.keytab"}
	t.Setenv("ONEBASE_PUBLIC_URL", service.PublicURL)
	t.Setenv("ONEBASE_WINDOWS_SPN", service.SPN)
	t.Setenv("ONEBASE_KRB5_REALM", service.Realm)
	t.Setenv("ONEBASE_KRB5_KEYTAB", service.Keytab)
	configured := DomainSettings{"https://new.example.org:8443", "HTTP/new.example.org", "", ""}
	check := func(repo *Repo, want DomainSettings) {
		t.Helper()
		got, err := repo.EffectiveDomainSettings(ctx)
		if err != nil || got != want {
			t.Fatalf("profile = %+v, err = %v, want %+v", got, err, want)
		}
	}
	check(repo, service)
	if err := repo.SaveDomainSettings(ctx, configured); err != nil {
		t.Fatal(err)
	}
	check(NewRepo(db), configured)
	check(other, service)
	if err := repo.SaveDomainSettings(ctx, DomainSettings{}); err != nil {
		t.Fatal(err)
	}
	check(repo, service)
	for _, raw := range []string{"null", "{", `{"public_url":"http://bad.example.org"}`} {
		if err := db.SaveSetting(ctx, domainSettingsKey, raw); err != nil {
			t.Fatal(err)
		}
		if got, err := repo.EffectiveDomainSettings(ctx); err == nil || got != (DomainSettings{}) {
			t.Fatalf("invalid stored profile selected %+v, err = %v", got, err)
		}
	}
}

func TestDomainSettingsValidation(t *testing.T) {
	base := DomainSettings{"https://erp.example.org", "HTTP/erp.example.org", "EXAMPLE.ORG", "/etc/onebase/erp.keytab"}
	for _, change := range []func(*DomainSettings){
		func(s *DomainSettings) { s.PublicURL = "http://erp.example.org" },
		func(s *DomainSettings) { s.PublicURL = "https://user@erp.example.org" },
		func(s *DomainSettings) { s.PublicURL += "/ui" },
		func(s *DomainSettings) { s.PublicURL += "?q=1" },
		func(s *DomainSettings) { s.PublicURL += "#fragment" },
		func(s *DomainSettings) { s.PublicURL += ":65536" },
		func(s *DomainSettings) { s.SPN = "HTTP/other.example.org" },
		func(s *DomainSettings) { s.Realm = "" },
		func(s *DomainSettings) { s.Realm = "invalid@realm" },
		func(s *DomainSettings) { s.Keytab = "" },
		func(s *DomainSettings) { s.Keytab = "erp.keytab" },
	} {
		s := base
		change(&s)
		if _, err := s.normalized(); !errors.Is(err, ErrInvalidDomainSettings) {
			t.Errorf("accepted invalid profile %+v", s)
		}
	}
	spaced := base
	spaced.PublicURL += "/"
	spaced.SPN += " "
	if got, err := spaced.normalized(); err != nil || got != base {
		t.Errorf("normalized profile = %+v, err = %v", got, err)
	}
}
