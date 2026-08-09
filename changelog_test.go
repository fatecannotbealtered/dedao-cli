package dedaocli

import (
	"encoding/json"
	"os"
	"testing"
)

func TestVersionMatchesPackageManifest(t *testing.T) {
	raw, err := os.ReadFile("package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if Version != manifest.Version {
		t.Fatalf("Version = %q, package.json version = %q", Version, manifest.Version)
	}
}
