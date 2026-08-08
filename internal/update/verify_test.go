package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// The checksum link binds the bytes on disk to the signed manifest. Every way
// it can fail must be terminal: an unverifiable release is never installed.
func TestVerifyArchiveDigest(t *testing.T) {
	archive := []byte("pretend this is a release archive")
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	checksums := []byte(digest + "  dedao-cli_1.0.0_linux_amd64.tar.gz\n" +
		strings.Repeat("0", 64) + "  other_asset.zip\n")

	if err := VerifyArchiveDigest(checksums, "dedao-cli_1.0.0_linux_amd64.tar.gz", archive); err != nil {
		t.Fatalf("a matching digest was rejected: %v", err)
	}

	// Tampered bytes.
	if err := VerifyArchiveDigest(checksums, "dedao-cli_1.0.0_linux_amd64.tar.gz", []byte("tampered")); err == nil {
		t.Error("a mismatched digest was accepted")
	} else if !errors.Is(err, ErrIntegrity) {
		t.Errorf("a mismatched digest is not an integrity failure: %v", err)
	}

	// An asset the manifest never covered is not implicitly trusted.
	if err := VerifyArchiveDigest(checksums, "unlisted.tar.gz", archive); err == nil {
		t.Error("an asset absent from checksums.txt was accepted")
	} else if !errors.Is(err, ErrIntegrity) {
		t.Errorf("a missing checksum entry is not an integrity failure: %v", err)
	}
}

// goreleaser writes binary-mode entries as `*name`; the parser has to read both.
func TestChecksumFor_AcceptsBinaryModeEntries(t *testing.T) {
	checksums := []byte("abc123  plain.tar.gz\ndef456 *binary.tar.gz\n")
	if got, ok := checksumFor(checksums, "plain.tar.gz"); !ok || got != "abc123" {
		t.Errorf("plain entry = %q,%v", got, ok)
	}
	if got, ok := checksumFor(checksums, "binary.tar.gz"); !ok || got != "def456" {
		t.Errorf("binary-mode entry = %q,%v", got, ok)
	}
}

// There is no "unsigned but probably fine" path. A release with no bundle must
// fail closed before any network fetch of the archive.
func TestVerifyChecksums_MissingBundleIsTerminal(t *testing.T) {
	err := VerifyChecksums("owner/repo", []byte("abc  x.tar.gz\n"), nil)
	if err == nil {
		t.Fatal("a release with no signature bundle was accepted")
	}
	if !errors.Is(err, ErrIntegrity) {
		t.Errorf("a missing bundle is not an integrity failure: %v", err)
	}
}

// A bundle that is not a Sigstore protobuf bundle -- including the legacy
// cosign format the spec explicitly does not accept -- must be rejected without
// reaching the network for trust material.
func TestVerifyChecksums_RejectsNonBundleInput(t *testing.T) {
	err := VerifyChecksums("owner/repo", []byte("abc  x.tar.gz\n"), []byte(`{"not":"a bundle"}`))
	if err == nil {
		t.Fatal("a malformed bundle was accepted")
	}
	if !errors.Is(err, ErrIntegrity) {
		t.Errorf("a malformed bundle is not an integrity failure: %v", err)
	}
}

// The signer identity has to be pinned to this repo's release workflow;
// otherwise any keyless signature from any workflow would satisfy the check.
func TestCertificateIdentity_PinsTheReleaseWorkflow(t *testing.T) {
	identity, err := certificateIdentity("owner/dedao-cli")
	if err != nil {
		t.Fatalf("building the identity policy failed: %v", err)
	}
	san := identity.SubjectAlternativeName.Regexp.String()
	if !strings.Contains(san, "owner/dedao-cli") {
		t.Errorf("the identity does not pin the repo: %s", san)
	}
	// The pattern is regexp source, so the literal dots arrive escaped.
	if !strings.Contains(san, `release\.yml`) {
		t.Errorf("the identity does not pin the release workflow: %s", san)
	}
	// Anchored on both ends, or a crafted SAN could carry the expected value as
	// a prefix and still match.
	if !strings.HasPrefix(san, "^") || !strings.HasSuffix(san, "$") {
		t.Errorf("the SAN pattern is not anchored: %s", san)
	}
	if issuer := identity.Issuer.Issuer; !strings.Contains(issuer, "token.actions.githubusercontent.com") {
		t.Errorf("issuer = %q, want GitHub's OIDC provider", issuer)
	}
}
