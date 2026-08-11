package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	dedaocli "github.com/fatecannotbealtered/dedao-cli"
	"github.com/fatecannotbealtered/dedao-cli/internal/dedao"
	getnoteapi "github.com/fatecannotbealtered/dedao-cli/internal/getnote"
	"github.com/fatecannotbealtered/dedao-cli/internal/output"
	"github.com/spf13/cobra"
)

var version = dedaocli.Version

// SkillMinVersion is the tool version the bundled Skill was written against.
// `doctor` compares it so a Skill that expects newer commands fails loudly
// rather than calling something that does not exist (CLI-SPEC §14).
var SkillMinVersion = dedaocli.Version

type application struct {
	in  io.Reader
	out io.Writer
	err io.Writer

	startedAt time.Time
	format    string
	jsonAlias bool
	compact   bool
	fields    []string
	quiet     bool
	limit     int
	// untrusted is the running command's declared untrusted field list.
	untrusted []string

	stateDir string
	timeout  time.Duration
}

// Execute is the process entry point.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	exit := ExecuteArgs(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(exit)
}

// ExecuteArgs runs one isolated invocation and returns its process exit code.
// Tests drive the CLI through this seam so they exercise the real command
// boundary rather than internal helpers.
func ExecuteArgs(ctx context.Context, args []string, in io.Reader, stdout, stderr io.Writer) int {
	app := &application{
		in:        in,
		out:       stdout,
		err:       stderr,
		startedAt: time.Now(),
		format:    output.FormatJSON,
	}
	root := app.rootCommand()
	root.SetArgs(args)
	root.SetIn(in)
	root.SetOut(stdout)
	root.SetErr(stderr)

	if err := root.ExecuteContext(ctx); err != nil {
		cliErr := asCLIError(err)
		if printErr := app.printer().Failure(cliErr); printErr != nil {
			_, _ = fmt.Fprintf(stderr, "failed to write error output: %v\n", printErr)
			return 1
		}
		// In text mode Failure() already wrote the human-readable line to
		// stdout; repeating it on stderr would just duplicate it.
		if !app.quiet && app.format == output.FormatJSON {
			_, _ = fmt.Fprintf(stderr, "%s: %s\n", cliErr.Code, cliErr.Message)
		}
		return cliErr.ExitCode()
	}
	return 0
}

func (a *application) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "dedao-cli",
		Short:         "Dedao reading and GetNote note management for AI agents",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.applyLimit(cmd); err != nil {
				return err
			}
			// Resolved here, from the leaf command about to run, so the marker
			// on `data` is exactly the list `reference` declares for it.
			a.untrusted = untrustedFieldsForCommand(cmd)
			return a.validateOutput(cmd)
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&a.format, "format", output.FormatJSON, "Output format: json, text, or raw")
	root.PersistentFlags().BoolVar(&a.jsonAlias, "json", false, "Compatibility alias for --format json")
	root.PersistentFlags().BoolVar(&a.compact, "compact", false, "Emit compact JSON")
	root.PersistentFlags().StringSliceVar(&a.fields, "fields", nil, "Return only these top-level data fields")
	root.PersistentFlags().BoolVar(&a.quiet, "quiet", false, "Suppress non-error stderr diagnostics")
	root.PersistentFlags().IntVar(&a.limit, "limit", 0, "Maximum number of list items to return")
	root.PersistentFlags().StringVar(&a.stateDir, "state-dir", "", "Session directory (default $DEDAO_HOME or ~/.dedao-api)")
	root.PersistentFlags().DurationVar(&a.timeout, "timeout", 0, "Upstream request timeout")

	for _, add := range []func() *cobra.Command{
		a.referenceCommand,
		a.contextCommand,
		a.doctorCommand,
		a.changelogCommand,
		a.statusCommand,
		a.loginCommand,
		a.loginResumeCommand,
		a.logoutCommand,
		a.articleCommand,
		a.articleCaptionsCommand,
		a.ebookChaptersCommand,
		a.ebookReadCommand,
		a.audiobookMediaCommand,
		a.dailyCommand,
		a.updateCommand,
		a.getnoteCommand,
	} {
		root.AddCommand(add())
	}
	root.AddCommand(a.queryCommands()...)
	return root
}

// validateOutput resolves the output mode before any command runs.
//
// Every rejection here resets the format to json first: the caller asked for a
// mode that cannot be honored, so reporting the failure in that same
// unresolvable mode would deny the agent the machine contract exactly when it
// needs to understand what went wrong.
func (a *application) validateOutput(cmd *cobra.Command) error {
	usage := func(message string) error {
		// Only the format is forced back: it is the setting that could not be
		// honored. --compact is orthogonal and still meaningful on an error
		// envelope, and silently ignoring it would surprise a caller who asked
		// for compact output. --fields is dropped because a projection only
		// applies to success data.
		a.format = output.FormatJSON
		a.fields = nil
		return output.NewError("E_USAGE", message, nil)
	}

	if a.jsonAlias {
		formatFlag := cmd.Flags().Lookup("format")
		if formatFlag != nil && formatFlag.Changed && !strings.EqualFold(strings.TrimSpace(a.format), output.FormatJSON) {
			return usage("--json conflicts with a non-json --format")
		}
		a.format = output.FormatJSON
	}
	a.format = strings.ToLower(strings.TrimSpace(a.format))
	if a.format == "" {
		a.format = output.FormatJSON
	}
	switch a.format {
	case output.FormatJSON, output.FormatText:
	case output.FormatRaw:
		if cmd.Annotations["raw_output"] != "true" {
			return usage(cmd.CommandPath() + " does not support --format raw")
		}
	default:
		return usage("--format must be one of: json, text, raw")
	}
	if a.compact && a.format != output.FormatJSON {
		return usage("--compact requires --format json")
	}
	if len(a.fields) > 0 && a.format != output.FormatJSON {
		return usage("--fields requires --format json")
	}
	return nil
}

func (a *application) printer() *output.Printer {
	return output.NewPrinter(a.out, output.Options{
		Format:    a.format,
		Compact:   a.compact,
		Fields:    a.fields,
		StartedAt: a.startedAt,
		Untrusted: a.untrusted,
		Notices:   cachedNotices(),
	})
}

func (a *application) success(data any) error {
	if err := a.printer().Success(data); err != nil {
		if errors.Is(err, output.ErrNormalization) {
			return output.WrapError("E_UNKNOWN", "failed to normalize command output", err,
				map[string]any{"normalization_error": err.Error()})
		}
		if len(a.fields) > 0 {
			return output.WrapError("E_VALIDATION", "invalid --fields selection", err, nil)
		}
		return output.WrapError("E_UNKNOWN", "failed to encode command output", err, nil)
	}
	return nil
}

// cachedNotices reads any stored update advisory.
//
// Read-only, from a local file, never the network: CLI-SPEC §14 is explicit
// that business commands surface a cached notice but must not phone home to
// advertise updates. An absent or stale cache yields nothing, and
// `meta.notices` is omitted.
func cachedNotices() []map[string]any {
	notice := updateConfig.ReadCachedNotice()
	if notice == nil {
		return nil
	}
	encoded, err := json.Marshal(notice)
	if err != nil {
		return nil
	}
	var asMap map[string]any
	if err := json.Unmarshal(encoded, &asMap); err != nil {
		return nil
	}
	return []map[string]any{asMap}
}

// baseURLOverride points the client at a mock upstream. It is unexported and
// set only by tests in this package, so it adds no public surface: production
// builds can never be steered at another host.
var baseURLOverride string

func (a *application) client() (*dedao.Client, error) {
	client, err := dedao.New(dedao.Options{
		StateDir: a.stateDir,
		Timeout:  a.timeout,
		BaseURL:  baseURLOverride,
	})
	if err != nil {
		return nil, output.WrapError("E_CONFIG", "could not initialize the Dedao client", err, nil)
	}
	return client, nil
}

// asCLIError classifies a failure by TYPE, never by sniffing message text: a
// response body containing the words "not found" must not become E_NOT_FOUND
// (CLI-SPEC §6).
func asCLIError(err error) *output.CLIError {
	var cliErr *output.CLIError
	if errors.As(err, &cliErr) {
		return cliErr
	}
	if errors.Is(err, dedao.ErrAuthRequired) {
		return output.WrapError("E_AUTH", err.Error(), err, nil)
	}
	if errors.Is(err, getnoteapi.ErrAuthRequired) {
		// One path, named once. `login` authorizes note access and mints the
		// credentials itself; the API-key command is the fallback for CI, where
		// nobody can approve an authorization.
		return output.WrapError("E_AUTH", err.Error(), err, map[string]any{
			"hint": "Run 'dedao-cli login' and relay the returned link and user code to the " +
				"person, then 'dedao-cli login-resume'.",
			"resume":            "dedao-cli login",
			"unattended_option": "dedao-cli getnote auth login --api-key-stdin --client-id <client-id>",
		})
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return output.WrapError("E_TIMEOUT", "operation timed out", err, nil)
	}
	if errors.Is(err, context.Canceled) {
		return output.WrapError("E_INTERRUPTED", "operation was interrupted", err, nil)
	}

	var apiErr *dedao.APIError
	if errors.As(err, &apiErr) {
		details := map[string]any{}
		code := "E_SERVER"
		if apiErr.StatusCode > 0 {
			details["status_code"] = apiErr.StatusCode
			code = output.CodeForStatus(apiErr.StatusCode)
		} else if apiErr.BusinessCode != nil {
			details["business_code"] = apiErr.BusinessCode
			code = businessCode(apiErr.BusinessCode)
		}
		return output.WrapError(code, apiErr.Error(), err, details)
	}
	var getnoteErr *getnoteapi.APIError
	if errors.As(err, &getnoteErr) {
		details := map[string]any{"hint": "Check GetNote credentials and the request; retry only for a retryable code."}
		code := "E_SERVER"
		if getnoteErr.BusinessCode != nil {
			details["business_code"] = getnoteErr.BusinessCode
			if businessCode, known := knownGetnoteBusinessCode(getnoteErr.BusinessCode); known {
				code = businessCode
			} else if getnoteErr.StatusCode > 0 {
				code = output.CodeForStatus(getnoteErr.StatusCode)
			}
		} else if getnoteErr.StatusCode > 0 {
			code = output.CodeForStatus(getnoteErr.StatusCode)
		}
		if getnoteErr.StatusCode > 0 {
			details["status_code"] = getnoteErr.StatusCode
		}
		if len(getnoteErr.Details) > 0 {
			details["upstream"] = getnoteErr.Details
			details["_untrusted"] = []string{"upstream"}
		}
		if code == "E_AUTH" {
			details["hint"] = "Run 'dedao-cli getnote auth login' with a valid GetNote API key and client ID."
		}
		return output.WrapError(code, getnoteErrorMessage(code), err, details)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return output.WrapError("E_TIMEOUT", "upstream request timed out", err, nil)
		}
		return output.WrapError("E_NETWORK", "upstream network request failed", err, nil)
	}
	return output.WrapError("E_USAGE", err.Error(), err, nil)
}

func getnoteErrorMessage(code string) string {
	switch code {
	case "E_AUTH":
		return "GetNote rejected the configured credentials"
	case "E_FORBIDDEN":
		return "GetNote denied access to the requested resource"
	case "E_NOT_FOUND":
		return "GetNote could not find the requested resource"
	case "E_VALIDATION":
		return "GetNote rejected the request"
	case "E_CONFLICT":
		return "GetNote reported a state conflict"
	case "E_RATE_LIMITED":
		return "GetNote rate-limited the request"
	default:
		return "GetNote upstream request failed"
	}
}

// businessCode classifies Dedao's in-band error codes.
//
// Everything used to fall through to E_SERVER, which is retryable. Code 104000
// is what an unknown identifier returns -- verified by mutating one character of
// a working course enid -- so an agent that trusted `retryable` would re-request
// a permanently missing resource forever. Dedao's own message for it reads
// "服务异常，请稍后重试", which is why classifying by message text rather than by
// code would get this exactly backwards (CLI-SPEC §6).
// Code 90015 is an entitlement wall: the audiobook-collection endpoint answers
// with it and "无权访问，请获得相应权限后再试" for a package this account has no
// subscription to, measured against two different packages. Falling through to
// E_SERVER made a permission boundary look like a service fault, and E_SERVER is
// retryable -- an agent would have hammered a wall that will never open. Saying
// "you do not have access" is the whole point of this tool.
func businessCode(value any) string {
	// Code 5218 is the audiobook detail endpoints' "no such product", reproduced
	// with both a real id of the wrong kind and an obviously invalid one. Like
	// 104000 it is permanent, so it must not arrive as a retryable fault.
	// Code 4000 is the ebook page endpoint's "this chapter has no body": front
	// matter such as a copyright page, and any chapter id that does not resolve.
	// Reproduced against a real front-matter chapter in two different books and
	// against an invented id. Permanent, like the two above.
	switch asInt(value) {
	case 104000, 5218, 4000:
		return "E_NOT_FOUND"
	case 90015:
		return "E_FORBIDDEN"
	}
	return "E_SERVER"
}

func knownGetnoteBusinessCode(value any) (string, bool) {
	switch asInt(value) {
	case 10000:
		return "E_VALIDATION", true
	case 10001, 10004:
		return "E_AUTH", true
	case 10100, 10500:
		return "E_NOT_FOUND", true
	case 10201:
		return "E_FORBIDDEN", true
	case 10202, 42900:
		return "E_RATE_LIMITED", true
	case 10502:
		return "E_CONFLICT", true
	default:
		return "", false
	}
}

func asInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	case string:
		if parsed, err := strconv.Atoi(typed); err == nil {
			return parsed
		}
	}
	return 0
}
