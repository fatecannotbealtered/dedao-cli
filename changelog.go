// Package dedaocli embeds the runtime metadata stored at the module root.
package dedaocli

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// ChangelogMarkdown is embedded from CHANGELOG.md, the single hand-maintained
// changelog source. The release notes and the runtime `changelog` command are
// both derived from it, so the two can never disagree (REPO-SPEC §4).
//
//go:embed CHANGELOG.md
var ChangelogMarkdown string

// packageManifest is also the runtime version source. Keeping the version in
// package.json avoids build-mode-dependent values from linker flags or git.
//
//go:embed package.json
var packageManifest []byte

// Version is the package.json version used by every runtime surface.
var Version = manifestVersion()

func manifestVersion() string {
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(packageManifest, &manifest); err != nil {
		panic("parse embedded package.json: " + err.Error())
	}
	if strings.TrimSpace(manifest.Version) == "" {
		panic("embedded package.json has no version")
	}
	return manifest.Version
}
