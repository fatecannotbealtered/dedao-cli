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
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Start a QR login and hand the code to a human",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}
			pending, err := client.BeginQRLogin(cmd.Context(), qrPath)
			if err != nil {
				return err
			}
			return output.NewError(
				"E_HUMAN_REQUIRED",
				"Show the QR image to the user and ask them to scan it in the Dedao app, then run login-resume.",
				map[string]any{
					"action":  "scan_qr",
					"qr_path": pending.QRPath,
					"resume":  "dedao-cli login-resume",
					"expires_hint": "Dedao login codes are short-lived; if login-resume reports " +
						"an expired code, run login again.",
				},
			)
		},
	}
	cmd.Flags().StringVar(&qrPath, "qr-path", "", "Where to write the QR image (default <state-dir>/login-qr.png)")
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
			pending, user, err := client.ResumeQRLogin(cmd.Context())
			switch {
			case errors.Is(err, dedao.ErrLoginPending):
				// Still waiting on the human. Same signal as `login`, so the
				// agent relays and waits instead of hammering the endpoint.
				return output.NewError(
					"E_HUMAN_REQUIRED",
					"The QR code has not been scanned yet. Ask the user to scan it, then run login-resume again.",
					map[string]any{
						"action":  "scan_qr",
						"qr_path": pending.QRPath,
						"resume":  "dedao-cli login-resume",
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
			return a.success(map[string]any{"logged_in": true, "user": user})
		},
	}
}
