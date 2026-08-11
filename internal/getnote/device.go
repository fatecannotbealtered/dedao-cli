package getnote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// The device flow is how a person authorizes this tool without ever opening a
// developer console: the server mints a short code, the person approves it on
// whatever device is handy, and the credentials arrive over the poll.
//
// Nothing here opens a browser. The verification link, the user code, and the
// server's own QR image are returned for a human to act on -- the same shape as
// the Dedao half, which writes a QR and hands it back.

// DeviceClientID is the public client id the official Get笔记 CLI registers for
// this flow. A device-flow client id names the application, not the account,
// and carries no secret; the server rejects unregistered values, so it is the
// one value a self-use tool cannot mint for itself. Callers that have
// registered their own application can pass it instead.
const DeviceClientID = "cli_a1b2c3d4e5f6789012345678abcdef90"

// Device authorization outcomes. `authorization_pending` arrives as a success
// envelope carrying a message rather than as an error, so it is decoded before
// the credentials are.
var (
	ErrDevicePending  = errors.New("the authorization is still pending")
	ErrDeviceExpired  = errors.New("the device code expired")
	ErrDeviceRejected = errors.New("the authorization was rejected")
	ErrDeviceConsumed = errors.New("the device code was already used")
)

// DeviceCode is one pending authorization. Code is the credential half and is
// never reported to the caller of the command.
type DeviceCode struct {
	Code            string `json:"code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	QRPath          string `json:"qr_path"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// DeviceCredentials is what a completed authorization mints.
type DeviceCredentials struct {
	APIKey    string `json:"api_key"`
	ClientID  string `json:"client_id"`
	ExpiresAt int64  `json:"expires_at"`
}

// postAnonymous performs one device-flow request. These two endpoints are the
// only ones that must work before any credential exists, so they deliberately
// bypass the Configured() guard the rest of the client is built on.
func (c *Client) postAnonymous(ctx context.Context, path string, body any) ([]byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, apiHTTPError(raw, resp.StatusCode, http.MethodPost, path, resp.Header)
	}
	return raw, nil
}

// deviceEnvelope is the shared shape of both device-flow responses.
type deviceEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
	} `json:"error"`
}

// RequestDeviceCode starts an authorization and, when the server supplies one,
// writes its QR image so the person can scan instead of typing a link.
func (c *Client) RequestDeviceCode(ctx context.Context, clientID, qrPath string) (DeviceCode, error) {
	if strings.TrimSpace(clientID) == "" {
		clientID = DeviceClientID
	}
	raw, err := c.postAnonymous(ctx, "/open/api/v1/oauth/device/code",
		map[string]any{"client_id": clientID})
	if err != nil {
		return DeviceCode{}, err
	}
	var envelope deviceEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return DeviceCode{}, &APIError{Message: "the authorization endpoint did not return JSON"}
	}
	if !envelope.Success {
		message := "the authorization could not be started"
		if envelope.Error != nil && envelope.Error.Reason == "not_found" {
			message = "the GetNote OAuth client id is not registered"
		}
		return DeviceCode{}, &APIError{Message: message}
	}
	var payload struct {
		Code            string `json:"code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		QRCode          string `json:"verification_uri_qrcode"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := json.Unmarshal(envelope.Data, &payload); err != nil || payload.Code == "" ||
		payload.VerificationURI == "" {
		return DeviceCode{}, &APIError{Message: "the authorization response was incomplete"}
	}
	device := DeviceCode{
		Code: payload.Code, UserCode: payload.UserCode,
		VerificationURI: payload.VerificationURI,
		ExpiresIn:       payload.ExpiresIn, Interval: payload.Interval,
	}
	if written := writeDataURIImage(payload.QRCode, qrPath); written != "" {
		device.QRPath = written
	}
	return device, nil
}

// writeDataURIImage saves a `data:image/png;base64,...` payload. A QR that
// cannot be written is not an error: the verification link still works.
func writeDataURIImage(dataURI, path string) string {
	if dataURI == "" || path == "" {
		return ""
	}
	encoded := dataURI
	if index := strings.Index(encoded, ","); index >= 0 {
		encoded = encoded[index+1:]
	}
	image, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ""
	}
	if err := os.WriteFile(path, image, 0o600); err != nil {
		return ""
	}
	return path
}

// PollDeviceToken checks once whether the person has authorized. One check, not
// a loop: an agent must relay the wait to a human rather than block on it.
func (c *Client) PollDeviceToken(ctx context.Context, clientID, code string) (DeviceCredentials, error) {
	if strings.TrimSpace(clientID) == "" {
		clientID = DeviceClientID
	}
	raw, err := c.postAnonymous(ctx, "/open/api/v1/oauth/token", map[string]any{
		"grant_type": "device_code", "client_id": clientID, "code": code,
	})
	if err != nil {
		return DeviceCredentials{}, err
	}
	var envelope deviceEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return DeviceCredentials{}, &APIError{Message: "the authorization endpoint did not return JSON"}
	}

	// The pending and terminal states arrive as a message inside an otherwise
	// successful envelope, so they are read before the credentials are.
	var state struct {
		Msg string `json:"msg"`
	}
	_ = json.Unmarshal(envelope.Data, &state)
	marker := state.Msg
	if envelope.Error != nil && marker == "" {
		marker = envelope.Error.Reason
	}
	switch {
	case strings.Contains(marker, "authorization_pending"):
		return DeviceCredentials{}, ErrDevicePending
	case strings.Contains(marker, "expired_token"):
		return DeviceCredentials{}, ErrDeviceExpired
	case strings.Contains(marker, "rejected"):
		return DeviceCredentials{}, ErrDeviceRejected
	case strings.Contains(marker, "already_consumed"):
		return DeviceCredentials{}, ErrDeviceConsumed
	}

	var credentials DeviceCredentials
	if err := json.Unmarshal(envelope.Data, &credentials); err != nil || credentials.APIKey == "" {
		return DeviceCredentials{}, &APIError{Message: "the authorization did not return credentials"}
	}
	if credentials.ClientID == "" {
		credentials.ClientID = clientID
	}
	return credentials, nil
}
