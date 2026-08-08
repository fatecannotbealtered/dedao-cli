package update

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The release workflow and the self-updater have to agree on one filename. They
// did not once: goreleaser wrote hyphens and the updater looked for
// underscores, so `update` could not find its own release and the only place
// that would have shown up was a live upgrade. This pins them together.
func TestAssetNamesMatchTheReleaseTemplate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yml: %v", err)
	}
	template := regexp.MustCompile(`name_template:\s*"([^"]+)"`).FindSubmatch(raw)
	if template == nil {
		t.Fatal(".goreleaser.yml declares no archive name_template")
	}
	// Render the template the way goreleaser would for this platform.
	replacer := strings.NewReplacer(
		"{{ .ProjectName }}", "dedao-cli",
		"{{ .Version }}", "1.2.3",
		"{{ .Os }}", runtime.GOOS,
		"{{ .Arch }}", runtime.GOARCH,
	)
	want := replacer.Replace(string(template[1]))
	if strings.Contains(want, "{{") {
		t.Fatalf("the archive name_template uses a field this test does not render: %q", want)
	}
	extension := ".tar.gz"
	if runtime.GOOS == "windows" {
		extension = ".zip"
	}
	want += extension

	got, _, _ := assetNames("dedao-cli", "1.2.3")
	if got != want {
		t.Errorf("self-update looks for %q but the release publishes %q", got, want)
	}
}

// The signature asset name has to match what the release workflow signs.
func TestChecksumAssetNamesMatchTheSigningStep(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yml: %v", err)
	}
	_, checksums, bundle := assetNames("dedao-cli", "1.2.3")
	if !strings.Contains(string(raw), "name_template: "+checksums) {
		t.Errorf(".goreleaser.yml does not publish %q", checksums)
	}
	// goreleaser writes the bundle as <artifact>.sigstore.json.
	if bundle != checksums+".sigstore.json" {
		t.Errorf("bundle name %q does not follow the signing step's ${artifact}.sigstore.json", bundle)
	}
	if !strings.Contains(string(raw), "${artifact}.sigstore.json") {
		t.Error(".goreleaser.yml no longer signs into ${artifact}.sigstore.json")
	}
}
