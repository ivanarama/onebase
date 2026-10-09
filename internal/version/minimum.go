package version

import (
	"cmp"
	"regexp"
	"strings"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
)

// MinimumWarning checks an optional configuration requirement against this
// binary. Unknown/non-semver builds are incomparable, never presumed newer.
// The returned diagnostic is advisory and must not be used to reject startup.
func MinimumWarning(required string) error {
	return MinimumWarningFor(String(), required)
}

// MinimumWarningFor also accepts the actual version of an already running base.
// A launcher may have been updated since that server process was started.
func MinimumWarningFor(current, required string) error {
	required = strings.TrimSpace(required)
	if required == "" {
		return nil
	}
	minimum, ok := parseMinimumVersion(required)
	if !ok {
		return i18nerr.Errorf("min_engine_version %q: ожидается версия major.minor.patch; работа продолжается", required)
	}
	installed, ok := parseMinimumVersion(current)
	if !ok {
		return i18nerr.Errorf("Нельзя сравнить версию платформы %q с min_engine_version %q; работа продолжается", current, required)
	}
	if installed.compare(minimum) < 0 {
		return i18nerr.Errorf("Конфигурации требуется платформа не ниже %s, установлена %s; работа продолжается", required, current)
	}
	return nil
}

// Keep components as decimal strings to avoid overflow for valid large versions.
var minimumVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

type minimumVersion struct {
	core []string
	pre  []string
}

func parseMinimumVersion(s string) (minimumVersion, bool) {
	m := minimumVersionPattern.FindStringSubmatch(s)
	if m == nil {
		return minimumVersion{}, false
	}
	v := minimumVersion{core: m[1:4]}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
		for _, part := range v.pre {
			if decimalIdentifier(part) && len(part) > 1 && part[0] == '0' {
				return minimumVersion{}, false
			}
		}
	}
	return v, true
}

func decimalIdentifier(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func compareDecimal(a, b string) int {
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func (v minimumVersion) compare(other minimumVersion) int {
	for i, part := range v.core {
		if c := compareDecimal(part, other.core[i]); c != 0 {
			return c
		}
	}
	if len(v.pre) == 0 || len(other.pre) == 0 {
		// A release sorts after a prerelease with the same core.
		if len(v.pre) == len(other.pre) {
			return 0
		}
		if len(v.pre) == 0 {
			return 1
		}
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(other.pre); i++ {
		a, b := v.pre[i], other.pre[i]
		an, bn := decimalIdentifier(a), decimalIdentifier(b)
		var c int
		switch {
		case an && bn:
			c = compareDecimal(a, b)
		case an:
			c = -1
		case bn:
			c = 1
		default:
			c = strings.Compare(a, b)
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(v.pre), len(other.pre))
}
