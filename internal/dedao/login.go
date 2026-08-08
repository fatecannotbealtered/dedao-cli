package dedao

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// PendingLogin is the handoff between starting a QR login and resuming it after
// a human has scanned. It is persisted because the two steps are separate
// process invocations: CLI-SPEC §16.3 forbids blocking the agent on a human.
type PendingLogin struct {
	OAuthToken   string `json:"oauth_token"`
	QRCodeString string `json:"qr_code_string"`
	QRPath       string `json:"qr_path"`
}

// ErrQRExpired means the code is dead and a fresh `login` is required.
var ErrQRExpired = fmt.Errorf("login QR code expired")

// ErrLoginPending means nobody has scanned yet; the caller should ask the human
// again rather than spin.
var ErrLoginPending = fmt.Errorf("login QR code has not been scanned yet")

// ErrNoPendingLogin means resume was called without a prior `login`.
var ErrNoPendingLogin = fmt.Errorf("no pending login; run `dedao-cli login` first")

func pendingPath(stateDir string) string {
	return filepath.Join(stateDir, "login-pending.json")
}

// oauthResponse is the QR endpoints' wrapper, which differs from the `h`/`c`
// envelope the content APIs use.
type oauthResponse struct {
	ErrCode int             `json:"errCode"`
	ErrMsg  string          `json:"errMsg"`
	Data    json.RawMessage `json:"data"`
}

// oauthAccessToken fetches the anonymous token the QR endpoints require. It
// deliberately does not go through requireAuth: this is the pre-login path.
func (c *Client) oauthAccessToken(ctx context.Context) (string, error) {
	if _, err := c.do(ctx, http.MethodGet, "/", nil, nil); err != nil {
		return "", err
	}
	raw, err := c.do(ctx, http.MethodPost, "/loginapi/getAccessToken", nil, nil)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", &APIError{Method: "POST", Path: "/loginapi/getAccessToken",
			Message: "anonymous OAuth access token was empty"}
	}
	return token, nil
}

func (c *Client) oauthRequest(ctx context.Context, method, path, token string, body any) (json.RawMessage, error) {
	raw, err := c.doWithToken(ctx, method, path, token, body)
	if err != nil {
		return nil, err
	}
	var parsed oauthResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, &APIError{Method: method, Path: path, Message: "login endpoint did not return JSON"}
	}
	if parsed.ErrCode != 0 {
		message := parsed.ErrMsg
		if strings.TrimSpace(message) == "" {
			message = fmt.Sprintf("login failed with code %d", parsed.ErrCode)
		}
		return nil, &APIError{Method: method, Path: path, Message: message, BusinessCode: parsed.ErrCode}
	}
	return parsed.Data, nil
}

// BeginQRLogin creates a login QR, writes the image, and records the pending
// state. It returns immediately: waiting for the scan is the caller's job, and
// the caller must hand the image to a human rather than poll blindly.
func (c *Client) BeginQRLogin(ctx context.Context, qrPath string) (PendingLogin, error) {
	token, err := c.oauthAccessToken(ctx)
	if err != nil {
		return PendingLogin{}, err
	}
	data, err := c.oauthRequest(ctx, http.MethodGet, "/oauth/api/embedded/qrcode", token, nil)
	if err != nil {
		return PendingLogin{}, err
	}
	var payload struct {
		QRCodeString string `json:"qrCodeString"`
		QRCode       string `json:"qrCode"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.QRCodeString == "" || payload.QRCode == "" {
		return PendingLogin{}, &APIError{Method: "GET", Path: "/oauth/api/embedded/qrcode",
			Message: "login QR response was incomplete"}
	}

	destination := qrPath
	if destination == "" {
		destination = filepath.Join(c.stateDir, "login-qr.png")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return PendingLogin{}, err
	}
	encoded := payload.QRCode
	if index := strings.Index(encoded, ","); index >= 0 {
		encoded = encoded[index+1:]
	}
	image, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return PendingLogin{}, &APIError{Method: "GET", Path: "/oauth/api/embedded/qrcode",
			Message: "login QR image was not valid base64"}
	}
	if err := os.WriteFile(destination, image, 0o600); err != nil {
		return PendingLogin{}, err
	}

	pending := PendingLogin{OAuthToken: token, QRCodeString: payload.QRCodeString, QRPath: destination}
	if err := c.savePendingLogin(pending); err != nil {
		return PendingLogin{}, err
	}
	return pending, nil
}

// ResumeQRLogin checks once whether the code has been scanned. One check, not a
// loop: an agent must relay the wait to a human instead of blocking.
func (c *Client) ResumeQRLogin(ctx context.Context) (PendingLogin, any, error) {
	pending, err := c.loadPendingLogin()
	if err != nil {
		return PendingLogin{}, nil, err
	}
	data, err := c.oauthRequest(ctx, http.MethodPost, "/oauth/api/embedded/qrcode/check_login",
		pending.OAuthToken, map[string]any{
			"pname":     "mobilesms",
			"scene":     "login",
			"qrCode":    pending.QRCodeString,
			"keepLogin": true,
		})
	if err != nil {
		return pending, nil, err
	}
	var payload struct {
		Status int `json:"status"`
	}
	_ = json.Unmarshal(data, &payload)

	switch payload.Status {
	case 1:
		user, err := c.requestJSON(ctx, http.MethodGet, "/api/pc/user/info", nil, nil)
		if err != nil {
			return pending, nil, err
		}
		if err := c.SaveSession(); err != nil {
			return pending, nil, err
		}
		c.clearPendingLogin()
		return pending, user, nil
	case 2:
		c.clearPendingLogin()
		return pending, nil, ErrQRExpired
	default:
		return pending, nil, ErrLoginPending
	}
}

func (c *Client) savePendingLogin(pending PendingLogin) error {
	if err := os.MkdirAll(c.stateDir, 0o700); err != nil {
		return err
	}
	encoded, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	return os.WriteFile(pendingPath(c.stateDir), encoded, 0o600)
}

func (c *Client) loadPendingLogin() (PendingLogin, error) {
	raw, err := os.ReadFile(pendingPath(c.stateDir))
	if err != nil {
		return PendingLogin{}, ErrNoPendingLogin
	}
	var pending PendingLogin
	if err := json.Unmarshal(raw, &pending); err != nil || pending.QRCodeString == "" {
		return PendingLogin{}, ErrNoPendingLogin
	}
	return pending, nil
}

func (c *Client) clearPendingLogin() {
	_ = os.Remove(pendingPath(c.stateDir))
}

// doWithToken issues a request carrying the anonymous OAuth token used by the
// login endpoints.
func (c *Client) doWithToken(ctx context.Context, method, path, token string, body any) ([]byte, error) {
	target := c.baseURL + path
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("xi-dt", "web")
	request.Header.Set("Referer", c.baseURL+"/")
	if token != "" {
		request.Header.Set("xi-oauth-token", token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		return nil, &APIError{StatusCode: response.StatusCode, Method: method, Path: path,
			Message: fmt.Sprintf("%s %s returned HTTP %d", method, path, response.StatusCode)}
	}
	return payload, nil
}
