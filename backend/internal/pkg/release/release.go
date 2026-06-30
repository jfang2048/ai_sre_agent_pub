package release

import "strings"

var (
	// These values are overridden at build time from the repo-root VERSION file.
	Version   = "v0.9"
	Commit    = "dev"
	BuildDate = "unknown"
)

func EffectiveVersion() string {
	if version := strings.TrimSpace(Version); version != "" {
		return version
	}
	return "v0.9"
}
