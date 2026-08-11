package cmd

import (
	"errors"

	"github.com/fatecannotbealtered/dedao-cli/internal/dedao"
	"github.com/fatecannotbealtered/dedao-cli/internal/output"
	"github.com/spf13/cobra"
)

// Login is the human-in-the-loop path from CLI-SPEC §16.3.
//
// `login` never blocks waiting for a scan. It mints the QR, writes the image,
// and returns E_HUMAN_REQUIRED (exit 9) with the action and the resume command.
// The agent's job is then to show the image to the person and stop -- polling on
// their behalf would burn the code and tell the user nothing.

func (a *application) loginCommand() *cobra.Command {
	var qrPath string
	var skipGetnote bool
	var oauthClientID string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Start a QR login and hand the code to a human",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}

			// Already signed in for content: minting a QR nobody will scan is
			// noise, and it would fail the whole command for someone whose only
			// missing half is note access. Go straight to what is left.
			if client.Authenticated() {
				if skipGetnote {
					return a.success(map[string]any{"logged_in": true, "already_signed_in": true})
				}
				getnote := a.beginGetnoteAuthorization(cmd.Context(), oauthClientID)
				if getnote["action"] == "authorize_getnote" {
					return output.NewError("E_HUMAN_REQUIRED",
						"Already signed in for content. Ask the user to open the GetNote link "+
							"and confirm the user code, then run login-resume.",
						map[string]any{
							"action":  "authorize_getnote",
							"resume":  "dedao-cli login-resume",
							"getnote": getnote,
						})
				}
				return a.success(map[string]any{
					"logged_in": true, "already_signed_in": true, "getnote": getnote,
				})
			}

			pending, err := client.BeginQRLogin(cmd.Context(), qrPath)
			if err != nil {
				return err
			}
			message := "Show the QR image to the user and ask them to scan it in the Dedao app, " +
				"then run login-resume."
			details := map[string]any{
				"action":  "scan_qr",
				"qr_path": pending.QRPath,
				"resume":  "dedao-cli login-resume",
				"expires_hint": "Dedao login codes are short-lived; if login-resume reports " +
					"an expired code, run login again.",
			}
			// The note half is authorized in the same pass so signing in is one
			// act, not two. It is started after the QR exists: a note
			// authorization that cannot start must not cost the person a session.
			if !skipGetnote {
				getnote := a.beginGetnoteAuthorization(cmd.Context(), oauthClientID)
				details["getnote"] = getnote
				if getnote["action"] == "authorize_getnote" {
					message = "Two approvals, one login: ask the user to scan the QR image in the " +
						"Dedao app and to open the GetNote link and confirm the user code. " +
						"Then run login-resume."
				}
			}
			return output.NewError("E_HUMAN_REQUIRED", message, details)
		},
	}
	cmd.Flags().StringVar(&qrPath, "qr-path", "", "Where to write the QR image (default <state-dir>/login-qr.png)")
	cmd.Flags().BoolVar(&skipGetnote, "skip-getnote", false,
		"Sign in for content only, without authorizing note access")
	cmd.Flags().StringVar(&oauthClientID, "oauth-client-id", "",
		"GetNote OAuth application client id (defaults to the published CLI client)")
	return cmd
}

func (a *application) loginResumeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "login-resume",
		Short: "Complete a QR login after the user has scanned",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}

			// Already signed in for content: there is no QR outstanding, because
			// `login` does not mint one in that state. Answering "no pending
			// login" here would strand the note authorization it did start.
			if client.Authenticated() {
				getnote, outstanding := a.resumeGetnoteAuthorization(cmd.Context())
				if outstanding {
					return output.NewError(
						"E_HUMAN_REQUIRED",
						"The GetNote authorization has not been approved yet. Ask the user to open "+
							"the link and confirm the user code, then run login-resume again.",
						map[string]any{
							"action":  "authorize_getnote",
							"resume":  "dedao-cli login-resume",
							"getnote": getnote,
						},
					)
				}
				return a.success(map[string]any{
					"logged_in": true, "already_signed_in": true, "getnote": getnote,
				})
			}

			pending, user, err := client.ResumeQRLogin(cmd.Context())
			switch {
			case errors.Is(err, dedao.ErrLoginPending):
				// Still waiting on the human. Same signal as `login`, so the
				// agent relays and waits instead of hammering the endpoint.
				getnote, _ := a.resumeGetnoteAuthorization(cmd.Context())
				return output.NewError(
					"E_HUMAN_REQUIRED",
					"The QR code has not been scanned yet. Ask the user to scan it, then run login-resume again.",
					map[string]any{
						"action":  "scan_qr",
						"qr_path": pending.QRPath,
						"resume":  "dedao-cli login-resume",
						"getnote": getnote,
					},
				)
			case errors.Is(err, dedao.ErrQRExpired):
				return output.NewError("E_CONFLICT",
					"The login QR code expired. Run `dedao-cli login` to mint a new one.",
					map[string]any{"action": "restart_login", "resume": "dedao-cli login"})
			case errors.Is(err, dedao.ErrNoPendingLogin):
				return output.NewError("E_CONFLICT", err.Error(),
					map[string]any{"action": "restart_login", "resume": "dedao-cli login"})
			case err != nil:
				return err
			}
			// The session is saved by now. If the note half is still waiting on
			// the person, say so rather than reporting a finished login -- but
			// an expired or skipped authorization resolves instead of waiting,
			// so this can never strand a caller who only wants content.
			getnote, outstanding := a.resumeGetnoteAuthorization(cmd.Context())
			if outstanding {
				return output.NewError(
					"E_HUMAN_REQUIRED",
					"Signed in for content. Ask the user to open the GetNote link and confirm the "+
						"user code, then run login-resume again.",
					map[string]any{
						"action":  "authorize_getnote",
						"resume":  "dedao-cli login-resume",
						"getnote": getnote,
					},
				)
			}
			return a.success(map[string]any{"logged_in": true, "user": user, "getnote": getnote})
		},
	}
}
