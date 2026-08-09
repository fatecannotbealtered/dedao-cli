package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// The standalone path: the tool owns its own file, so it may replace it -- but
// only after the release verifies. Order matters and is not negotiable:
//
//	fetch checksums.txt + bundle -> verify signature -> fetch archive ->
//	verify digest -> extract -> atomic swap
//
// Nothing touches the installed binary until both verification steps have
// passed, so a failed or interrupted download leaves the working install alone.

// maxAssetBytes bounds every download so a hostile release cannot exhaust
// memory on the way to being rejected.
const maxAssetBytes = 200 << 20

// assetNames returns the archive and checksum asset names goreleaser publishes.
//
// The separator is a hyphen because that is what `.goreleaser.yml` writes
// (`{{ .ProjectName }}-{{ .Version }}-{{ .Os }}-{{ .Arch }}`). A test pins the
// two together: they disagreed once, and the only symptom was that self-update
// could not find its own release.
func assetNames(tool, version string) (archive, checksums, bundleName string) {
	extension := "tar.gz"
	if runtime.GOOS == "windows" {
		extension = "zip"
	}
	archive = fmt.Sprintf("%s-%s-%s-%s.%s", tool, version, runtime.GOOS, runtime.GOARCH, extension)
	return archive, "checksums.txt", "checksums.txt.sigstore.json"
}

func downloadAsset(ctx context.Context, client *http.Client, release *Release, name string) ([]byte, error) {
	for _, asset := range release.Assets {
		if asset.Name != name {
			continue
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s", ErrAssetMissing, name)
		}
		if rateLimitedResponse(response) {
			return nil, ErrRateLimited
		}
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("downloading %s answered HTTP %d", name, response.StatusCode)
		}
		return io.ReadAll(io.LimitReader(response.Body, maxAssetBytes))
	}
	return nil, fmt.Errorf("%w: %s", ErrAssetMissing, name)
}

// extractBinary pulls the tool's executable out of a verified archive.
//
// Entry names are checked rather than trusted: an archive that has already been
// verified is authentic, not necessarily well-formed, and a path like
// `../../bin/sh` would escape the destination during extraction.
func extractBinary(archive []byte, tool string) ([]byte, error) {
	wanted := tool
	if runtime.GOOS == "windows" {
		wanted += ".exe"
	}
	if runtime.GOOS == "windows" {
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, file := range reader.File {
			if path.Base(filepath.ToSlash(file.Name)) != wanted {
				continue
			}
			opened, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = opened.Close() }()
			return io.ReadAll(io.LimitReader(opened, maxAssetBytes))
		}
		return nil, fmt.Errorf("the archive holds no %s", wanted)
	}

	gzipped, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gzipped.Close() }()
	reader := tar.NewReader(gzipped)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if path.Base(filepath.ToSlash(header.Name)) != wanted {
			continue
		}
		return io.ReadAll(io.LimitReader(reader, maxAssetBytes))
	}
	return nil, fmt.Errorf("the archive holds no %s", wanted)
}

// replaceExecutable swaps the running binary for a verified one.
//
// The new file is written beside the old and renamed over it, so the swap is
// atomic and a crash mid-write cannot leave a truncated executable. Windows
// refuses to replace a running image, so the old one is moved aside first and
// removed on the next run.
func replaceExecutable(target string, contents []byte) error {
	directory := filepath.Dir(target)
	staged, err := os.CreateTemp(directory, ".update-*")
	if err != nil {
		return err
	}
	stagedName := staged.Name()
	defer func() { _ = os.Remove(stagedName) }()

	if _, err := staged.Write(contents); err != nil {
		_ = staged.Close()
		return err
	}
	if err := staged.Sync(); err != nil {
		_ = staged.Close()
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	if err := os.Chmod(stagedName, 0o755); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		previous := target + ".old"
		_ = os.Remove(previous)
		if err := os.Rename(target, previous); err != nil {
			return err
		}
		if err := os.Rename(stagedName, target); err != nil {
			// Put the working binary back rather than leaving nothing behind.
			_ = os.Rename(previous, target)
			return err
		}
		return nil
	}
	return os.Rename(stagedName, target)
}

// installBinary runs the whole verified-replacement sequence.
func (c Config) installBinary(ctx context.Context, client *http.Client, release *Release, result *Result) error {
	archiveName, checksumsName, bundleName := assetNames(c.Tool, release.Version)

	result.Stage = "download"
	checksums, err := downloadAsset(ctx, client, release, checksumsName)
	if err != nil {
		if errors.Is(err, ErrAssetMissing) {
			return fmt.Errorf("%w: checksums.txt is required for verification", ErrIntegrity)
		}
		return err
	}
	signature, err := downloadAsset(ctx, client, release, bundleName)
	if err != nil {
		if errors.Is(err, ErrAssetMissing) {
			// A release with no signature bundle is not installable. There is no
			// "unsigned but probably fine" path (SEC-SPEC §5).
			return fmt.Errorf("%w: signature bundle is required for verification", ErrIntegrity)
		}
		return err
	}

	result.Stage = "verify_signature"
	targetTag := "v" + strings.TrimPrefix(release.Version, "v")
	if err := VerifyChecksums(ctx, c.Repo, targetTag, checksums, signature); err != nil {
		return err
	}
	result.SignatureStatus = "verified"
	result.SignatureVerify = true

	result.Stage = "download"
	archive, err := downloadAsset(ctx, client, release, archiveName)
	if err != nil {
		if errors.Is(err, ErrAssetMissing) {
			return fmt.Errorf("%w: archive is absent from the signed release", ErrIntegrity)
		}
		return err
	}

	result.Stage = "verify_checksum"
	if err := VerifyArchiveDigest(checksums, archiveName, archive); err != nil {
		return err
	}
	result.ChecksumStatus = "verified"

	result.Stage = "replace"
	binary, err := extractBinary(archive, c.Tool)
	if err != nil {
		return err
	}

	result.Stage = "replace"
	target := currentExecutable()
	if target == "" {
		return fmt.Errorf("could not locate the running executable to replace")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := replaceExecutable(target, binary); err != nil {
		return err
	}
	result.BinaryReplaced = true
	result.CurrentVersion = release.Version
	return nil
}

var _ = strings.TrimSpace
