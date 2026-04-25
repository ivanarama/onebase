package version

import "runtime/debug"

// Build is set via -ldflags="-X github.com/ivantit66/onebase/internal/version.Build=v1.2.3"
var Build = ""

// String returns the platform version string.
func String() string {
	if Build != "" {
		return Build
	}
	// Fall back to VCS info embedded by go build
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return "dev-" + s.Value[:7]
			}
		}
	}
	return "dev"
}
