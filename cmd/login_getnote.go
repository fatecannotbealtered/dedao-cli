package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	getnoteapi "github.com/fatecannotbealtered/dedao-cli/internal/getnote"
	"github.com/fatecannotbealtered/dedao-cli/internal/secret"
)

// One login, both halves.
//
// Content and notes are separate services with separate credentials, but that
// is an upstream fact the person signing in should not have to work around. So
// `login` starts both authorizations and `login-resume` settles both, each with
// the same human-in-the-loop shape: hand something to a person, return, and let
// them act. Neither half blocks, and neither can strand the other.

const getnotePendingDeviceSecret = "getnote-device-authorization"

// getnotePendingDevice is a started-but-unapproved authorization. The code is
// credential material -- it is what mints an API key -- so it is sealed in the
// same encrypted store as the key itself and never reported to the caller.
type getnotePendingDevice struct {
	ClientID        string `json:"client_id"`
	Code            string `json:"code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	QRPath          string `json:"qr_path"`
}

func (a *application) saveGetnotePendingDevice(store *secret.Store, pending getnotePendingDevice) error {
	encoded, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	return store.Save(getnotePendingDeviceSecret, encoded)
}

func loadGetnotePendingDevice(store *secret.Store) (getnotePendingDevice, bool) {
	raw, err := store.Load(getnotePendingDeviceSecret)
	if err != nil {
		return getnotePendingDevice{}, false
	}
	var pending getnotePendingDevice
	if err := json.Unmarshal(raw, &pending); err != nil || pending.Code == "" {
		return getnotePendingDevice{}, false
	}
	return pending, true
}

// beginGetnoteAuthorization starts the note half of a login.
//
// It never fails the login: the Dedao half has already succeeded by the time it
// runs, and a note authorization that could not be started is reported so the
// person can retry it rather than losing the session they just created.
func (a *application) beginGetnoteAuthorization(
	ctx context.Context, oauthClientID string,
) map[string]any {
	apiKey, clientID, _, err := a.loadGetnoteCredentials()
	if err == nil && apiKey != "" && clientID != "" {
		return map[string]any{"authorized": true, "already_configured": true}
	}

	client, stateDir, err := a.getnoteClient()
	if err != nil {
		return map[string]any{"authorized": false, "unavailable": true,
			"reason": "could not resolve the GetNote state directory"}
	}
	device, err := client.RequestDeviceCode(ctx, oauthClientID,
		filepath.Join(stateDir, "authorize-qr.png"))
	if err != nil {
		return map[string]any{"authorized": false, "unavailable": true,
			"reason": asCLIError(err).Message}
	}
	pending := getnotePendingDevice{
		ClientID: oauthClientID, Code: device.Code, UserCode: device.UserCode,
		VerificationURI: device.VerificationURI, QRPath: device.QRPath,
	}
	if err := a.saveGetnotePendingDevice(secret.New(stateDir), pending); err != nil {
		return map[string]any{"authorized": false, "unavailable": true,
			"reason": "could not record the pending GetNote authorization"}
	}
	return map[string]any{
		"authorized":       false,
		"action":           "authorize_getnote",
		"verification_uri": device.VerificationURI,
		"user_code":        device.UserCode,
		"qr_path":          emptyToNilString(device.QRPath),
		"resume":           "dedao-cli login-resume",
	}
}

// resumeGetnoteAuthorization settles the note half, reporting whether a human
// still has to act. An expired code resolves rather than waits: a login that
// could never complete is worse than one that reports what it could not do.
func (a *application) resumeGetnoteAuthorization(ctx context.Context) (map[string]any, bool) {
	apiKey, clientID, stateDir, err := a.loadGetnoteCredentials()
	if err == nil && apiKey != "" && clientID != "" {
		return map[string]any{"authorized": true}, false
	}
	store := secret.New(stateDir)
	pending, ok := loadGetnotePendingDevice(store)
	if !ok {
		// Nothing was started -- `login --skip-getnote`, or a tool used only for
		// content. Not an outstanding action.
		return map[string]any{"authorized": false, "pending": false}, false
	}

	client, _, err := a.getnoteClient()
	if err != nil {
		return map[string]any{"authorized": false, "pending": true,
			"reason": "could not build a GetNote client"}, true
	}
	credentials, err := client.PollDeviceToken(ctx, pending.ClientID, pending.Code)
	switch {
	case errors.Is(err, getnoteapi.ErrDevicePending):
		return map[string]any{
			"authorized": false, "pending": true,
			"action":           "authorize_getnote",
			"verification_uri": pending.VerificationURI,
			"user_code":        pending.UserCode,
			"qr_path":          emptyToNilString(pending.QRPath),
			"resume":           "dedao-cli login-resume",
		}, true
	case errors.Is(err, getnoteapi.ErrDeviceExpired),
		errors.Is(err, getnoteapi.ErrDeviceConsumed),
		errors.Is(err, getnoteapi.ErrDeviceRejected):
		_ = store.Delete(getnotePendingDeviceSecret)
		return map[string]any{"authorized": false, "pending": false,
			"reason": err.Error(),
			"retry":  "dedao-cli login"}, false
	case err != nil:
		return map[string]any{"authorized": false, "pending": true,
			"reason": asCLIError(err).Message,
			"resume": "dedao-cli login-resume"}, true
	}

	if err := store.Save(getnoteAPIKeySecret, []byte(credentials.APIKey)); err != nil {
		return map[string]any{"authorized": false, "pending": true,
			"reason": "could not store the GetNote API key"}, true
	}
	if err := store.Save(getnoteClientIDSecret, []byte(credentials.ClientID)); err != nil {
		_ = store.Delete(getnoteAPIKeySecret)
		return map[string]any{"authorized": false, "pending": true,
			"reason": "could not store the GetNote client ID"}, true
	}
	_ = store.Delete(getnotePendingDeviceSecret)
	result := map[string]any{"authorized": true}
	if credentials.ExpiresAt > 0 {
		// The output layer normalizes `*_at` to RFC3339 UTC, so the raw epoch
		// value is handed over rather than formatted twice.
		result["expires_at"] = credentials.ExpiresAt
	}
	return result, false
}

func emptyToNilString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
