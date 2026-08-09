package cmd

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fatecannotbealtered/dedao-cli/internal/output"
)

const getnoteConfirmTTL = 5 * time.Minute

func getnoteConfirmToken(stateDir, command string, payload, guard any) (string, time.Time, error) {
	expires := time.Now().UTC().Add(getnoteConfirmTTL).Truncate(time.Second)
	secret, err := getnoteConfirmSecret(stateDir, true)
	if err != nil {
		return "", time.Time{}, err
	}
	nonceBytes := make([]byte, 8)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", time.Time{}, err
	}
	nonce := hex.EncodeToString(nonceBytes)
	guardMAC, err := getnoteConfirmMAC(secret, command, guard, expires.Unix(), nonce)
	if err != nil {
		return "", time.Time{}, err
	}
	payloadMAC, err := getnoteConfirmMAC(secret, command, payload, expires.Unix(), nonce)
	if err != nil {
		return "", time.Time{}, err
	}
	return "gct_" + strconv.FormatInt(expires.Unix(), 10) + "_" + nonce + "_" + guardMAC + "_" + payloadMAC, expires, nil
}

func getnoteConfirmMAC(secret []byte, command string, payload any, expires int64, nonce string) (string, error) {
	seed, err := json.Marshal(struct {
		Command string `json:"command"`
		Payload any    `json:"payload"`
		Expires int64  `json:"expires_unix"`
		Nonce   string `json:"nonce"`
	}{command, payload, expires, nonce})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(seed)
	return hex.EncodeToString(mac.Sum(nil)[:16]), nil
}

func validateGetnoteConfirmGuard(token, stateDir, command string, guard any) error {
	if token == "" {
		return output.NewError("E_CONFIRMATION_REQUIRED", "confirmation required: run this command with --dry-run, then retry with --confirm <confirm_token>", nil)
	}
	parts := strings.Split(token, "_")
	if len(parts) != 5 || parts[0] != "gct" || len(parts[2]) != 16 {
		return output.NewError("E_CONFLICT", "confirmation token is invalid or expired; re-run with --dry-run", nil)
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || !time.Now().UTC().Before(time.Unix(expires, 0).UTC()) {
		return output.NewError("E_CONFLICT", "confirmation token is invalid or expired; re-run with --dry-run", nil)
	}
	secret, err := getnoteConfirmSecret(stateDir, false)
	if err != nil {
		return output.NewError("E_CONFLICT", "confirmation token is invalid or expired; re-run with --dry-run", nil)
	}
	expected, err := getnoteConfirmMAC(secret, command, guard, expires, parts[2])
	if err != nil {
		return output.NewError("E_CONFLICT", "confirmation token is invalid or expired; re-run with --dry-run", nil)
	}
	if !hmac.Equal([]byte(parts[3]), []byte(expected)) {
		return output.NewError("E_CONFLICT", "confirmation token does not match the preview; re-run with --dry-run", nil)
	}
	return nil
}

func validateGetnoteConfirmToken(token, stateDir, command string, payload, guard any) error {
	if err := validateGetnoteConfirmGuard(token, stateDir, command, guard); err != nil {
		return err
	}
	parts := strings.Split(token, "_")
	expires, _ := strconv.ParseInt(parts[1], 10, 64)
	secret, err := getnoteConfirmSecret(stateDir, false)
	if err != nil {
		return output.NewError("E_CONFLICT", "confirmation token is invalid or expired; re-run with --dry-run", nil)
	}
	expected, err := getnoteConfirmMAC(secret, command, payload, expires, parts[2])
	if err != nil || !hmac.Equal([]byte(parts[4]), []byte(expected)) {
		return output.NewError("E_CONFLICT", "confirmation token does not match the preview; re-run with --dry-run", nil)
	}
	return consumeGetnoteConfirmToken(token, stateDir)
}

func getnoteConfirmSecret(stateDir string, create bool) ([]byte, error) {
	path := filepath.Join(stateDir, "confirm.secret")
	if raw, err := os.ReadFile(path); err == nil && len(raw) == 32 {
		return raw, nil
	} else if !create {
		return nil, errors.New("confirmation secret is missing")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return getnoteConfirmSecret(stateDir, false)
	}
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return raw, nil
}

func consumeGetnoteConfirmToken(token, stateDir string) error {
	digest := sha256.Sum256([]byte(token))
	dir := filepath.Join(stateDir, "confirm-consumed")
	path := filepath.Join(dir, hex.EncodeToString(digest[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		// CLI-SPEC §9: replay tracking is a safety enhancement, but a local
		// ledger failure must not turn a valid confirmed write into an outage.
		return nil
	}
	pruneGetnoteConfirmTokens(dir)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return output.NewError("E_CONFLICT", "confirmation token was already used; re-run with --dry-run", nil)
	}
	if err != nil {
		return nil
	}
	parts := strings.Split(token, "_")
	if len(parts) >= 2 {
		_, _ = file.WriteString(parts[1])
	}
	_ = file.Close()
	return nil
}

func pruneGetnoteConfirmTokens(dir string) {
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
