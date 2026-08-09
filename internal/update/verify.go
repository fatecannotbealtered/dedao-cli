package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// Release integrity is verified in-process (SEC-SPEC §5): no external cosign,
// nothing pre-installed on the machine, and no skip path. The chain is
//
//	Sigstore bundle over checksums.txt  ->  sha256 of the archive
//
// Both links must hold. A missing bundle, a signature that does not verify, a
// checksum line that is absent, or a digest that does not match are all the
// same answer -- do not install this -- and all surface as ErrIntegrity, which
// is non-retryable because a forged artifact does not improve on a second try.

// certificateIdentity pins who is allowed to have signed a release.
//
// Keyless signing proves *someone* signed it; without pinning the identity, any
// GitHub Actions workflow anywhere would satisfy the check. The issuer is
// GitHub's OIDC provider and the SAN is this repo's release workflow.
func certificateIdentity(repo, targetTag string) (verify.CertificateIdentity, error) {
	// A regexp, anchored, with the repo and workflow path escaped: the second
	// argument is regexp source, so an unescaped `.` would match any character
	// and a bare `*` would not mean "any tag" at all.
	if strings.TrimSpace(targetTag) == "" {
		return verify.CertificateIdentity{}, fmt.Errorf("target release tag is empty")
	}
	san := fmt.Sprintf(`^%s$`, regexp.QuoteMeta(fmt.Sprintf(
		"https://github.com/%s/.github/workflows/release.yml@refs/tags/%s", repo, targetTag)))
	matcher, err := verify.NewSANMatcher("", san)
	if err != nil {
		return verify.CertificateIdentity{}, err
	}
	issuer, err := verify.NewIssuerMatcher("https://token.actions.githubusercontent.com", "")
	if err != nil {
		return verify.CertificateIdentity{}, err
	}
	// No further extension constraints: SAN plus issuer already pins the
	// workflow file in this repo, and over-constraining would break on a
	// routine Actions change without buying more assurance.
	return verify.NewCertificateIdentity(matcher, issuer, certificate.Extensions{})
}

// trustedRoot bootstraps Sigstore's trust material from the library's embedded
// root, not from a first-use fetch: TOFU would make the first update the
// weakest one.
func trustedRootOptions(ctx context.Context) *tuf.Options {
	return tuf.DefaultOptions().WithContext(ctx)
}

func trustedRoot(ctx context.Context) (root.TrustedMaterial, error) {
	options := trustedRootOptions(ctx)
	live, err := root.NewLiveTrustedRoot(options)
	if err != nil {
		return nil, trustedRootError(ctx, err)
	}
	return live, nil
}

func trustedRootError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// VerifyChecksums checks the Sigstore bundle over checksums.txt.
//
// checksumsBundle is the protobuf bundle produced by
// `cosign sign-blob --new-bundle-format`; the legacy cosign format is not
// accepted, which is why parsing is strict rather than best-effort.
func VerifyChecksums(ctx context.Context, repo, targetTag string, checksums, checksumsBundle []byte) error {
	if len(checksumsBundle) == 0 {
		return fmt.Errorf("%w: the release carries no signature bundle for checksums.txt", ErrIntegrity)
	}
	var signed bundle.Bundle
	if err := signed.UnmarshalJSON(checksumsBundle); err != nil {
		return fmt.Errorf("%w: the signature bundle is not a Sigstore protobuf bundle: %v",
			ErrIntegrity, err)
	}

	material, err := trustedRoot(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// Trust material we cannot establish is not a reason to proceed.
		return fmt.Errorf("%w: could not establish Sigstore trust material: %v", ErrIntegrity, err)
	}
	verifier, err := verify.NewVerifier(material,
		verify.WithObserverTimestamps(1),
		verify.WithTransparencyLog(1),
	)
	if err != nil {
		return fmt.Errorf("%w: could not build the verifier: %v", ErrIntegrity, err)
	}
	identity, err := certificateIdentity(repo, targetTag)
	if err != nil {
		return fmt.Errorf("%w: could not build the signer identity policy: %v", ErrIntegrity, err)
	}
	policy := verify.NewPolicy(
		verify.WithArtifact(strings.NewReader(string(checksums))),
		verify.WithCertificateIdentity(identity),
	)
	if _, err := verifier.Verify(&signed, policy); err != nil {
		return fmt.Errorf("%w: the checksums signature did not verify: %v", ErrIntegrity, err)
	}
	return nil
}

// VerifyArchiveDigest checks one archive against the verified checksum file.
//
// Called only after VerifyChecksums has passed, so the checksum file is
// trusted; this is the second link that binds the bytes on disk to it.
func VerifyArchiveDigest(checksums []byte, assetName string, archive []byte) error {
	want, ok := checksumFor(checksums, assetName)
	if !ok {
		return fmt.Errorf("%w: checksums.txt has no entry for %s", ErrIntegrity, assetName)
	}
	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%w: %s hashes to %s but checksums.txt says %s",
			ErrIntegrity, assetName, got, want)
	}
	return nil
}

// checksumFor reads one `<sha256>  <name>` line out of a checksums file.
func checksumFor(checksums []byte, assetName string) (string, bool) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		// goreleaser writes `*name` for binary mode.
		name := strings.TrimPrefix(fields[1], "*")
		if name == assetName {
			return fields[0], true
		}
	}
	return "", false
}
