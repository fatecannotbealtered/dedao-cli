package cmd

import (
	"errors"
	"net/http"
	"os"
	"time"

	dedaocli "github.com/fatecannotbealtered/dedao-cli"
	"github.com/fatecannotbealtered/dedao-cli/internal/output"
	"github.com/fatecannotbealtered/dedao-cli/internal/update"
	"github.com/spf13/cobra"
)

// updateConfig describes this tool to the shared update layer.
var updateConfig = update.Config{
	Tool:       "dedao-cli",
	Repo:       "fatecannotbealtered/dedao-cli",
	NPMPackage: "@fateforge/dedao-cli",
	Version:    version,
	CacheEnv:   "DEDAO_UPDATE_CACHE_DIR",
	Changelog:  dedaocli.ChangelogMarkdown,
}

func updateHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

// updateCommand performs the whole update in one call.
//
// There is no confirm token and there are no leaf subcommands: CLI-SPEC §14
// exempts self-update from the §7 write gate because it is single-target and
// self-verifying. The safety guarantee is the integrity check, not an agent
// reviewing a preview.
func (a *application) updateCommand() *cobra.Command {
	var check, dryRun bool
	var targetVersion string

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the CLI and sync the bundled Skill in one call",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client := updateHTTPClient(a.timeout)

			if check {
				notice, err := updateConfig.Check(cmd.Context(), client)
				// "No release published yet" is an answer: report it as a
				// successful check with nothing available, not as an
				// unreachable network an agent should retry.
				if errors.Is(err, update.ErrNoRelease) {
					notice, err = nil, nil
				}
				if err != nil {
					return output.WrapError("E_NETWORK",
						"could not reach the release feed to check for updates", err, nil)
				}
				return a.success(map[string]any{
					"current_version":      version,
					"install_method":       string(update.DetectInstallMethod(executablePath())),
					"update_available":     notice != nil,
					"latest_version":       noticeLatest(notice, version),
					"skill_sync_supported": true,
					"signature_available":  true,
					"notice":               noticeOrNil(notice),
				})
			}

			result, err := updateConfig.Run(cmd.Context(), client, targetVersion, dryRun)
			if err != nil {
				return updateFailure(result, err)
			}
			return a.success(result)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Read-only probe: report whether an update is available")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Read-only preview of what an update would do")
	cmd.Flags().StringVar(&targetVersion, "target-version", "", "Update to this version instead of the latest")
	return cmd
}

// updateFailure maps an update error onto the contract, keeping the partial
// state visible so an agent always knows which version it is on.
func updateFailure(result *update.Result, err error) error {
	details := map[string]any{}
	if result != nil {
		details["stage"] = result.Stage
		details["current_version"] = result.CurrentVersion
		details["binary_replaced"] = result.BinaryReplaced
		details["skill_sync_status"] = result.SkillSyncStatus
		if result.SkillSyncCommand != "" {
			details["skill_sync_command"] = result.SkillSyncCommand
		}
	}
	switch {
	case errors.Is(err, update.ErrNoRelease):
		// Nothing to install, and nothing a retry would change.
		return output.WrapError("E_NOT_FOUND",
			"this repository publishes no release to update to", err, details)
	case isIntegrityFailure(err):
		// Non-retryable on purpose: a release that fails to verify will fail
		// again, and retrying a forged artifact is the wrong instinct.
		return output.WrapError("E_INTEGRITY", err.Error(), err, details)
	default:
		return output.WrapError("E_SERVER", err.Error(), err, details)
	}
}

func isIntegrityFailure(err error) bool {
	return err != nil && errors.Is(err, update.ErrIntegrity)
}

// executablePath reports the running binary, which is how the install method is
// detected. An unresolvable path yields "", which reads as MethodUnknown.
func executablePath() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return path
}

func noticeLatest(notice *update.Notice, fallback string) string {
	if notice == nil {
		return fallback
	}
	return notice.LatestVersion
}

func noticeOrNil(notice *update.Notice) any {
	if notice == nil {
		return nil
	}
	return notice
}
