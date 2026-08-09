package dedao

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/fatecannotbealtered/dedao-cli/internal/secret"
)

// cookieRecord is the on-disk shape of one persisted cookie.
type cookieRecord struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Domain  string `json:"domain"`
	Path    string `json:"path"`
	Secure  bool   `json:"secure"`
	Expires int64  `json:"expires,omitempty"`
}

// StateDir resolves the session directory: explicit flag, then DEDAO_HOME, then
// the per-user default.
func StateDir(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env := os.Getenv("DEDAO_HOME"); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".dedao-api"), nil
}

func cookiePath(stateDir string) string {
	return filepath.Join(stateDir, "cookies.json")
}

// sessionSecret is the name the session is sealed under in the secret store.
const sessionSecret = "session"

// CredentialFingerprint irreversibly identifies all credential material that
// logout will remove. Cookie order is normalized so the same target state is
// stable while any current or legacy credential change invalidates the token.
func (c *Client) CredentialFingerprint() string {
	base, err := url.Parse(c.baseURL + "/")
	if err != nil {
		return ""
	}
	type credentialPart struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	parts := make([]credentialPart, 0)
	for _, cookie := range c.jar.Cookies(base) {
		parts = append(parts, credentialPart{Name: cookie.Name, Value: cookie.Value})
	}
	sort.Slice(parts, func(i, j int) bool {
		if parts[i].Name != parts[j].Name {
			return parts[i].Name < parts[j].Name
		}
		return parts[i].Value < parts[j].Value
	})
	hash := sha256.New()
	encoded, _ := json.Marshal(parts)
	_, _ = hash.Write(encoded)
	for _, path := range LegacyPlaintextFiles(c.stateDir) {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(filepath.Base(path)))
		_, _ = hash.Write([]byte{0})
		if raw, err := os.ReadFile(path); err == nil {
			digest := sha256.Sum256(raw)
			_, _ = hash.Write(digest[:])
		} else if info, statErr := os.Stat(path); statErr == nil {
			metadata, _ := json.Marshal([]any{info.Size(), info.ModTime().UTC().UnixNano()})
			_, _ = hash.Write(metadata)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// loadCookies restores a persisted session. A missing or unreadable file is not
// an error: the caller simply ends up unauthenticated.
//
// A session written by an earlier build is plaintext JSON; it is adopted into
// the encrypted store on first read and the plaintext original removed, so the
// upgrade needs no user action and leaves nothing readable behind.
func loadCookies(jar *cookiejar.Jar, stateDir string) {
	store := secret.New(stateDir)
	raw, err := store.Load(sessionSecret)
	if err != nil {
		adopted, ok := store.AdoptPlaintext(sessionSecret, cookiePath(stateDir))
		if !ok {
			return
		}
		raw = adopted
	}
	var records []cookieRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return
	}
	byHost := map[string][]*http.Cookie{}
	for _, record := range records {
		host := record.Domain
		if len(host) > 0 && host[0] == '.' {
			host = host[1:]
		}
		if host == "" {
			host = "www.dedao.cn"
		}
		cookie := &http.Cookie{
			Name:   record.Name,
			Value:  record.Value,
			Path:   record.Path,
			Domain: record.Domain,
			Secure: record.Secure,
		}
		if record.Expires > 0 {
			cookie.Expires = time.Unix(record.Expires, 0)
		}
		byHost[host] = append(byHost[host], cookie)
	}
	for host, cookies := range byHost {
		if parsed, err := url.Parse("https://" + host + "/"); err == nil {
			jar.SetCookies(parsed, cookies)
		}
	}
}

// saveCookies persists the session, writing through a temp file so an
// interrupted write can never leave a truncated session behind, and tightening
// permissions before the file is put in place.
func saveCookies(jar *cookiejar.Jar, stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	base, err := url.Parse(BaseURL + "/")
	if err != nil {
		return err
	}
	records := []cookieRecord{}
	for _, cookie := range jar.Cookies(base) {
		records = append(records, cookieRecord{
			Name:   cookie.Name,
			Value:  cookie.Value,
			Domain: base.Host,
			Path:   "/",
			Secure: cookie.Secure,
		})
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		return err
	}
	// The session is an account-level credential, so it is sealed rather than
	// written as JSON (SEC-SPEC §4). The store owns the atomic write.
	return secret.New(stateDir).Save(sessionSecret, encoded)
}

func clearCookies(stateDir string) error {
	if err := secret.New(stateDir).Delete(sessionSecret); err != nil {
		return err
	}
	// Logout means "this machine no longer holds my credentials". A plaintext
	// copy left behind by an earlier build would make that false, so every
	// known plaintext location is removed too, not just the current one.
	for _, path := range LegacyPlaintextFiles(stateDir) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// legacyPlaintextNames are the files that have ever held this account's
// credentials in the clear. Nothing reads them any more, so one left on disk is
// pure exposure.
var legacyPlaintextNames = []string{
	"cookies.json", // the pre-encryption session, shared with the reference build
}

// LegacyPlaintextFiles lists the plaintext credential files present in a state
// directory. `doctor` reports them and `logout` removes them.
func LegacyPlaintextFiles(stateDir string) []string {
	var found []string
	for _, name := range legacyPlaintextNames {
		path := filepath.Join(stateDir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			found = append(found, path)
		}
	}
	return found
}

// SessionBackend names where the session is held, for `context` and `doctor`.
func SessionBackend(stateDir string) string {
	return secret.New(stateDir).Backend()
}
