package cmd

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fatecannotbealtered/dedao-cli/internal/output"
)

const logoutConfirmTTL = 5 * time.Minute

type logoutConfirmPayload struct {
	Action                string `json:"action"`
	CommandPath           string `json:"command_path"`
	StateDir              string `json:"state_dir"`
	CredentialConfigured  bool   `json:"credential_configured"`
	CredentialFingerprint string `json:"credential_fingerprint"`
	PermissionContext     string `json:"permission_context"`
	// The GetNote half is part of the payload so the token cannot authorise a
	// preview of one credential set and then delete a different one. When the
	// note credentials are being kept they are left out of the fingerprint --
	// binding to something this run will not touch would turn an unrelated
	// re-authorization into a spurious conflict -- and `KeepGetnote` records the
	// narrower scope so a token minted for one cannot execute the other.
	KeepGetnote        bool   `json:"keep_getnote"`
	GetnoteConfigured  bool   `json:"getnote_configured"`
	GetnoteFingerprint string `json:"getnote_fingerprint"`
}

func logoutPayload(stateDir string, configured bool, fingerprint string) logoutConfirmPayload {
	return logoutConfirmPayload{
		Action:                "delete credentials",
		CommandPath:           "dedao-cli logout",
		StateDir:              stateDir,
		CredentialConfigured:  configured,
		CredentialFingerprint: fingerprint,
		PermissionContext:     "local-credential-store",
	}
}

func newLogoutConfirmToken(payload logoutConfirmPayload) (string, time.Time, error) {
	expires := time.Now().UTC().Add(logoutConfirmTTL).Truncate(time.Second)
	secret, err := loadOrCreateLogoutConfirmSecret(payload.StateDir)
	if err != nil {
		return "", time.Time{}, err
	}
	token, err := buildLogoutConfirmToken(secret, payload, expires.Unix())
	return token, expires, err
}

func validateLogoutConfirmToken(token string, payload logoutConfirmPayload) error {
	if token == "" {
		return output.NewError("E_CONFIRMATION_REQUIRED",
			"confirmation required: run logout with --dry-run, then retry with --confirm <confirm_token>", nil)
	}
	parts := strings.Split(token, "_")
	if len(parts) != 3 || parts[0] != "ct" {
		return invalidLogoutConfirmToken()
	}
	expiresUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || !time.Now().UTC().Before(time.Unix(expiresUnix, 0).UTC()) {
		return invalidLogoutConfirmToken()
	}
	secret, err := readLogoutConfirmSecret(payload.StateDir)
	if err != nil {
		return invalidLogoutConfirmToken()
	}
	expected, err := buildLogoutConfirmToken(secret, payload, expiresUnix)
	if err != nil || !hmac.Equal([]byte(token), []byte(expected)) {
		return invalidLogoutConfirmToken()
	}
	return nil
}

func invalidLogoutConfirmToken() error {
	return output.NewError("E_CONFLICT", "confirmation token is invalid or expired; re-run logout with --dry-run", nil)
}

func consumeLogoutConfirmToken(token, stateDir string) error {
	dir := filepath.Join(stateDir, "confirm-consumed")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	pruneConsumedLogoutTokens(dir)
	digest := sha256.Sum256([]byte(token))
	path := filepath.Join(dir, hex.EncodeToString(digest[:]))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return output.NewError("E_CONFLICT",
			"confirmation token was already used; re-run logout with --dry-run", nil)
	}
	if err != nil {
		return nil
	}
	parts := strings.Split(token, "_")
	if len(parts) == 3 {
		_, _ = file.WriteString(parts[1])
	}
	_ = file.Close()
	return nil
}

func pruneConsumedLogoutTokens(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := time.Now().UTC().Unix()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		expires, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err == nil && expires <= now {
			_ = os.Remove(path)
		}
	}
}

func buildLogoutConfirmToken(secret []byte, payload logoutConfirmPayload, expiresUnix int64) (string, error) {
	seed, err := json.Marshal(struct {
		Payload     logoutConfirmPayload `json:"payload"`
		ExpiresUnix int64                `json:"expires_unix"`
	}{Payload: payload, ExpiresUnix: expiresUnix})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(seed)
	return fmt.Sprintf("ct_%d_%s", expiresUnix, hex.EncodeToString(mac.Sum(nil)[:16])), nil
}

func logoutConfirmSecretPath(stateDir string) string {
	return filepath.Join(stateDir, "confirm.secret")
}

func readLogoutConfirmSecret(stateDir string) ([]byte, error) {
	secret, err := os.ReadFile(logoutConfirmSecretPath(stateDir))
	if err != nil {
		return nil, err
	}
	if len(secret) != 32 {
		return nil, errors.New("invalid confirm secret")
	}
	return secret, nil
}

func loadOrCreateLogoutConfirmSecret(stateDir string) ([]byte, error) {
	if secret, err := readLogoutConfirmSecret(stateDir); err == nil {
		return secret, nil
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(logoutConfirmSecretPath(stateDir), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return readLogoutConfirmSecret(stateDir)
	}
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(secret); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return secret, nil
}
