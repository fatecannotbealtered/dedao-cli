// Package update implements the self-update and version-notification contract
// (CLI-SPEC §14, REPO-SPEC §4, SEC-SPEC §5).
//
// Three rules shape everything here:
//
//   - A bare `update` owns the whole lifecycle in one call. There is no confirm
//     token and there are no leaf subcommands: self-update is single-target and
//     self-verifying, so the safety guarantee is the signature check below, not
//     an agent reviewing a preview.
//   - Integrity failures are terminal. A missing signature bundle, a signature
//     that does not verify, or a checksum mismatch all fail closed as
//     `E_INTEGRITY` with no "proceed anyway" path -- a forged release is not a
//     transient blip to retry.
//   - Only maintenance commands may touch the network. `context` and `--help`
//     read the local cache and never phone home, so a business command can
//     surface a notice without turning every invocation into a registry call.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config describes the tool being updated. Each repo fills this in once.
type Config struct {
	Tool       string // binary name, e.g. "dedao-cli"
	Repo       string // "owner/name" on GitHub
	NPMPackage string // published npm package, e.g. "@fateforge/dedao-cli"
	Version    string // the running version
	// CacheEnv names the environment variable that relocates the notice cache.
	// Tests set it so a compiled test binary never reads or writes the real
	// user cache (REPO-SPEC §4).
	CacheEnv string
	// Changelog is the embedded CHANGELOG, used to grade notice severity from
	// the delta between the running version and the latest.
	Changelog string
	// Method overrides install-method detection. Production leaves it empty and
	// detects from the running executable; tests set it so a case can exercise
	// the npm path without being run from an npm-managed location.
	Method InstallMethod
}

// installMethod resolves the method for this run.
func (c Config) installMethod() InstallMethod {
	if c.Method != "" {
		return c.Method
	}
	return DetectInstallMethod(currentExecutable())
}

// InstallMethod is how the running binary got onto the machine, which decides
// how it may be replaced.
type InstallMethod string

const (
	// MethodNPM means a package manager owns the file. Mutating it in place
	// would desync the manager's metadata, so the manager is driven instead.
	MethodNPM InstallMethod = "npm"
	// MethodBinary means the tool owns its own file and may replace it after
	// verifying the release signature.
	MethodBinary InstallMethod = "binary"
	// MethodUnknown means neither could be established; the update refuses
	// rather than guessing at how to replace someone else's file.
	MethodUnknown InstallMethod = "unknown"
)

// DetectInstallMethod inspects the running executable's path.
//
// Misclassifying a standalone binary as managed would print an npm command that
// does nothing; misclassifying a managed install as standalone would overwrite
// a file npm believes it owns. Both are worse than refusing, so anything
// ambiguous is MethodUnknown.
func DetectInstallMethod(executable string) InstallMethod {
	// An unknown path is unknown, not a standalone binary. EvalSymlinks("")
	// answers "." on some platforms, which would otherwise read as a real file.
	if strings.TrimSpace(executable) == "" {
		return MethodUnknown
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		resolved = executable
	}
	normalized := strings.ReplaceAll(filepath.ToSlash(resolved), "\\", "/")
	for _, marker := range []string{"/node_modules/", "/npm/", "/.npm/", "/npm-global/"} {
		if strings.Contains(normalized, marker) {
			return MethodNPM
		}
	}
	if strings.Contains(normalized, "/go/bin/") || strings.Contains(normalized, "/gopath/bin/") {
		return MethodBinary
	}
	if resolved != "" {
		return MethodBinary
	}
	return MethodUnknown
}

// Release is the upstream view of the newest published version.
type Release struct {
	Version string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Notice is the structured update advisory an agent consumes.
type Notice struct {
	Type               string   `json:"type"`
	Severity           string   `json:"severity"`
	CurrentVersion     string   `json:"current_version"`
	LatestVersion      string   `json:"latest_version"`
	InstallMethod      string   `json:"install_method"`
	RecommendedCommand string   `json:"recommended_command"`
	ReleaseURL         string   `json:"release_url,omitempty"`
	CheckedAt          string   `json:"checked_at"`
	NextSteps          []string `json:"next_steps"`
}

// cachePath is where the notice cache lives. It is deliberately separate from
// the session state directory: a notice is not a credential, and mixing the two
// would put a network-derived file next to the account's secrets.
func (c Config) cachePath() (string, error) {
	if override := os.Getenv(c.CacheEnv); override != "" {
		return filepath.Join(override, "update-notice.json"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, c.Tool, "update-notice.json"), nil
}

// ReadCachedNotice returns the stored advisory, if any. It never touches the
// network, which is what makes it safe to attach to every command's
// `meta.notices`.
func (c Config) ReadCachedNotice() *Notice {
	path, err := c.cachePath()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var notice Notice
	if err := json.Unmarshal(raw, &notice); err != nil {
		return nil
	}
	// A notice for a version we are already on is stale by definition; the spec
	// requires it be suppressed rather than repeated after a successful update.
	if notice.LatestVersion == "" || !NewerThan(notice.LatestVersion, c.Version) {
		return nil
	}
	return &notice
}

func (c Config) writeCache(notice *Notice) error {
	path, err := c.cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if notice == nil {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	encoded, err := json.Marshal(notice)
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o600)
}

// ClearCache drops any stored advisory, used after a successful update so later
// commands stop attaching a notice for the version now installed.
func (c Config) ClearCache() error { return c.writeCache(nil) }

// ErrNoRelease means the repository has no published release to compare
// against. It is a definite answer, so callers report it as such rather than as
// an unreachable network.
var ErrNoRelease = errors.New("the repository publishes no release yet")

// ErrRateLimited marks a release service refusal that should be retried only
// after backing off.
var ErrRateLimited = errors.New("the release service rate-limited the request")

// ErrAssetMissing means a release artifact named by the release metadata is
// absent. Required verification artifacts turn this into ErrIntegrity; a
// transport error remains retryable.
var ErrAssetMissing = errors.New("release asset is missing")

func rateLimitedResponse(response *http.Response) bool {
	return response.StatusCode == http.StatusTooManyRequests ||
		(response.StatusCode == http.StatusForbidden &&
			response.Header.Get("X-RateLimit-Remaining") == "0")
}

// releaseAPI is the GitHub endpoint for the newest release.
func (c Config) releaseAPI() string {
	return "https://api.github.com/repos/" + c.Repo + "/releases/latest"
}

// FetchLatest asks GitHub for the newest release. Only maintenance commands
// call this.
func (c Config) FetchLatest(ctx context.Context, client *http.Client) (*Release, error) {
	return c.fetchLatestFrom(ctx, client, c.releaseAPI())
}

// fetchLatestFrom is FetchLatest with the endpoint named, so a test can serve
// the refusals GitHub gives without reaching GitHub.
func (c Config) fetchLatestFrom(ctx context.Context, client *http.Client, endpoint string) (*Release, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", c.Tool+"/"+c.Version)

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	// A 404 is an answer, not a failure to get one: the repository publishes no
	// release yet. Reporting it as a network problem would have an agent retry
	// a condition that cannot change until someone tags a release.
	if response.StatusCode == http.StatusNotFound {
		return nil, ErrNoRelease
	}
	if rateLimitedResponse(response) {
		return nil, ErrRateLimited
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the release feed answered HTTP %d", response.StatusCode)
	}
	var release Release
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&release); err != nil {
		return nil, err
	}
	release.Version = strings.TrimPrefix(release.Version, "v")
	return &release, nil
}

// Check refreshes the advisory and returns it. This is the only path that both
// reaches the network and writes the cache.
func (c Config) Check(ctx context.Context, client *http.Client) (*Notice, error) {
	release, err := c.FetchLatest(ctx, client)
	if err != nil {
		return nil, err
	}
	if !NewerThan(release.Version, c.Version) {
		// Clear rather than leave a stale advisory behind.
		_ = c.ClearCache()
		return nil, nil
	}
	method := c.installMethod()
	notice := &Notice{
		Type:               "update_available",
		Severity:           GradeSeverity(c.Changelog, c.Version, release.Version),
		CurrentVersion:     c.Version,
		LatestVersion:      release.Version,
		InstallMethod:      string(method),
		RecommendedCommand: c.Tool + " update",
		ReleaseURL:         release.HTMLURL,
		CheckedAt:          time.Now().UTC().Format(time.RFC3339),
		NextSteps: []string{
			c.Tool + " update",
			c.Tool + " changelog --since " + c.Version,
			c.Tool + " reference --compact",
		},
	}
	_ = c.writeCache(notice)
	return notice, nil
}

func currentExecutable() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return path
}

// NewerThan compares two semantic versions.
func NewerThan(candidate, current string) bool {
	return compareVersions(candidate, current) > 0
}

func compareVersions(a, b string) int {
	aParts, bParts := versionParts(a), versionParts(b)
	for i := 0; i < 3; i++ {
		if aParts[i] != bParts[i] {
			if aParts[i] > bParts[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionParts(value string) [3]int {
	var parts [3]int
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	if index := strings.IndexAny(value, "-+"); index >= 0 {
		value = value[:index]
	}
	for i, segment := range strings.SplitN(value, ".", 3) {
		if i > 2 {
			break
		}
		parts[i], _ = strconv.Atoi(segment)
	}
	return parts
}

// GradeSeverity computes how urgent an update is from the changelog delta.
//
// `warning` when the delta carries a security entry or crosses a major version;
// otherwise `info`. Grading at check time and storing the result is what lets a
// cached notice carry the right level without re-reading the changelog.
func GradeSeverity(changelog, current, latest string) string {
	if versionParts(latest)[0] > versionParts(current)[0] {
		return "warning"
	}
	if changelogDeltaHasSecurity(changelog, current, latest) {
		return "warning"
	}
	return "info"
}

// changelogDeltaHasSecurity reports whether any release newer than `current`
// and no newer than `latest` carries a Security section with content.
func changelogDeltaHasSecurity(changelog, current, latest string) bool {
	var version string
	inSecurity := false
	for _, line := range strings.Split(changelog, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "## "):
			version = releaseHeadingVersion(trimmed)
			inSecurity = false
		case strings.HasPrefix(trimmed, "### "):
			inSecurity = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(trimmed, "### ")), "Security")
		case inSecurity && strings.HasPrefix(trimmed, "- "):
			if version == "" {
				continue
			}
			if NewerThan(version, current) && compareVersions(version, latest) <= 0 {
				return true
			}
		}
	}
	return false
}

// releaseHeadingVersion pulls the version out of `## [1.2.3] - 2026-01-01`.
// `Unreleased` yields "" so it is never graded.
func releaseHeadingVersion(heading string) string {
	heading = strings.TrimPrefix(heading, "## ")
	heading = strings.TrimSpace(heading)
	heading = strings.TrimPrefix(heading, "[")
	if index := strings.IndexAny(heading, "]"); index >= 0 {
		heading = heading[:index]
	}
	heading = strings.TrimSpace(heading)
	if strings.EqualFold(heading, "Unreleased") {
		return ""
	}
	if versionParts(heading) == [3]int{0, 0, 0} && !strings.HasPrefix(heading, "0.0.0") {
		return ""
	}
	return heading
}

// Result is what `update` returns. Every field describes the final
// post-command state, not the pre-install comparison (REPO-SPEC §4).
type Result struct {
	Status           string `json:"status"`
	Stage            string `json:"stage"`
	PreviousVersion  string `json:"previous_version"`
	CurrentVersion   string `json:"current_version"`
	TargetVersion    string `json:"target_version"`
	UpdateAvailable  bool   `json:"update_available"`
	InstallMethod    string `json:"install_method"`
	Command          string `json:"command,omitempty"`
	BinaryReplaced   bool   `json:"binary_replaced"`
	SignatureStatus  string `json:"signature_status"`
	SignatureVerify  bool   `json:"signature_verified"`
	ChecksumStatus   string `json:"checksum_status"`
	SkillSyncStatus  string `json:"skill_sync_status"`
	SkillSyncCommand string `json:"skill_sync_command,omitempty"`
	ReleaseURL       string `json:"release_url,omitempty"`
	Notice           any    `json:"notice,omitempty"`
}

// ErrIntegrity marks a verification failure. It maps to E_INTEGRITY, which is
// non-retryable: a release that fails to verify will fail again.
var ErrIntegrity = errors.New("release integrity verification failed")

// ErrInstallMethod means the executable cannot be updated safely until the
// installation is repaired or repeated through a supported package manager.
var ErrInstallMethod = errors.New("unsupported installation method")

// CommandError preserves the exact argv command and its typed cause so the
// CLI can provide a safe recovery command without guessing from stderr text.
type CommandError struct {
	Command string
	Cause   error
}

func (e *CommandError) Error() string {
	if e == nil || e.Cause == nil {
		return ""
	}
	return e.Command + " failed: " + e.Cause.Error()
}

func (e *CommandError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// SkillSyncCommand is the command that brings the bundled Skill directory to
// the same end state as a fresh install.
func (c Config) SkillSyncCommand() string {
	return "npx skills add " + c.Repo + " -y -g"
}

// runner lets tests observe the commands that would be driven without running
// a package manager for real.
type runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	// argv form, never a shell string: an agent-supplied version must not be
	// able to reach a shell (SEC-SPEC §3).
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Run performs the whole update in one call.
func (c Config) Run(ctx context.Context, client *http.Client, targetVersion string, dryRun bool) (*Result, error) {
	return c.run(ctx, client, targetVersion, dryRun, execRunner)
}

func (c Config) run(ctx context.Context, client *http.Client, targetVersion string, dryRun bool, run runner) (*Result, error) {
	method := c.installMethod()
	result := &Result{
		Status:          "noop",
		Stage:           "discover",
		PreviousVersion: c.Version,
		CurrentVersion:  c.Version,
		InstallMethod:   string(method),
		SignatureStatus: "not_checked",
		ChecksumStatus:  "not_checked",
		SkillSyncStatus: "not_attempted",
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	target := strings.TrimPrefix(targetVersion, "v")
	if target == "" {
		release, err := c.FetchLatest(ctx, client)
		if err != nil {
			return result, err
		}
		target = release.Version
		result.ReleaseURL = release.HTMLURL
	}
	result.TargetVersion = target

	// The idempotent no-op check runs before any package-manager command, so an
	// already-current install never shells out (REPO-SPEC §4).
	if !NewerThan(target, c.Version) {
		result.Status = "current"
		result.Stage = "complete"
		result.UpdateAvailable = false
		_ = c.ClearCache()
		return result, nil
	}
	result.UpdateAvailable = true
	result.Command = c.Tool + " update --target-version " + target
	if method == MethodNPM {
		result.Command = fmt.Sprintf("npm install -g %s@%s", c.NPMPackage, target)
	}

	if dryRun {
		result.Status = "dry_run"
		result.Stage = "preview"
		result.SkillSyncCommand = c.SkillSyncCommand()
		return result, nil
	}

	switch method {
	case MethodNPM:
		result.Stage = "download"
		// Drive the manager rather than printing the command: the contract is
		// that one call reaches the upgraded end state.
		if output, err := run(ctx, "npm", "install", "-g", c.NPMPackage+"@"+target); err != nil {
			result.Status = "failed"
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, ctxErr
			}
			return result, &CommandError{Command: result.Command,
				Cause: fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))}
		}
		// Integrity on this path is the registry's own provenance, so there is
		// no signature for the tool to check and it says so rather than
		// implying it verified something.
		result.SignatureStatus = "not_checked"
		result.ChecksumStatus = "registry_provenance"
		result.BinaryReplaced = true
		// The new version takes effect on the next invocation; this process is
		// still the old binary, so `current_version` reports the installed one.
		result.CurrentVersion = target

	case MethodBinary:
		// The release metadata is needed for the asset urls; re-resolve when the
		// caller pinned a version rather than taking the latest.
		release, err := c.releaseFor(ctx, client, target)
		if err != nil {
			result.Status = "failed"
			return result, err
		}
		if err := c.installBinary(ctx, client, release, result); err != nil {
			result.Status = "failed"
			return result, err
		}

	default:
		result.Status = "failed"
		return result, fmt.Errorf("%w: "+
			"could not tell how %s was installed, so it will not replace the file; "+
			"reinstall with `npm install -g %s@%s`", ErrInstallMethod, c.Tool, c.NPMPackage, target)
	}

	result.Stage = "skill_sync"
	if ctxErr := ctx.Err(); ctxErr != nil {
		result.Status = "partial"
		result.SkillSyncStatus = "failed"
		result.SkillSyncCommand = c.SkillSyncCommand()
		return result, ctxErr
	}
	if output, err := run(ctx, "npx", "skills", "add", c.Repo, "-y", "-g"); err != nil {
		// Partial success: the binary moved, the Skill did not. The agent must
		// not use newly documented behavior until it has (CLI-SPEC §14).
		result.Status = "partial"
		result.SkillSyncStatus = "failed"
		result.SkillSyncCommand = c.SkillSyncCommand()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		return result, &CommandError{Command: result.SkillSyncCommand,
			Cause: fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))}
	}
	result.SkillSyncStatus = "synced"
	result.Stage = "complete"
	result.Status = "updated"
	result.UpdateAvailable = false
	_ = c.ClearCache()
	return result, nil
}

// releaseFor resolves the release metadata for a specific version.
//
// GitHub's "latest" endpoint answers only for the newest tag, so a pinned
// --target-version needs the by-tag lookup.
func (c Config) releaseFor(ctx context.Context, client *http.Client, version string) (*Release, error) {
	latest, err := c.FetchLatest(ctx, client)
	if err == nil && latest.Version == version {
		return latest, nil
	}
	url := "https://api.github.com/repos/" + c.Repo + "/releases/tags/v" + version
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", c.Tool+"/"+c.Version)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusNotFound {
			return nil, ErrNoRelease
		}
		if rateLimitedResponse(response) {
			return nil, ErrRateLimited
		}
		return nil, fmt.Errorf("no release is published for version %s", version)
	}
	var release Release
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&release); err != nil {
		return nil, err
	}
	release.Version = strings.TrimPrefix(release.Version, "v")
	return &release, nil
}

// Platform reports the release asset naming for this host, used by the
// standalone path.
func Platform() string { return runtime.GOOS + "_" + runtime.GOARCH }
