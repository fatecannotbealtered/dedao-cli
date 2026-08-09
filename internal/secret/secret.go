// Package secret stores credential-shaped data at rest.
//
// SEC-SPEC §4 wants secrets in the OS keyring, with an encrypted file as a
// visible fallback where no keyring service exists (headless Linux, CI). This
// implements exactly that, with one wrinkle worth recording: the keyring holds
// a 32-byte data key, never the payload. Windows Credential Manager caps a
// credential blob at 2560 bytes and a Dedao cookie jar is already larger than
// that, so storing the session directly would fail on the platform most likely
// to have a keyring at all.
//
// So the payload is always an AES-256-GCM file. Only the key source changes:
//
//	keyring        the key is random, generated once, kept by the OS
//	encrypted-file the key is derived from machine-bound factors
//
// The fallback is honest about what it buys: its factors are enumerable, so it
// defeats copying the state directory to another machine, not code already
// running as this user. `context` and `doctor` report which backend is live so
// that degradation is never silent.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zalando/go-keyring"
)

// keyringService is the account namespace this tool owns in the OS keyring.
const keyringService = "dedao-cli"

// backendEnv forces a backend. Tests set it to "file" so `go test` never writes
// to the developer's real credential store; users may set it for the same
// reason on a shared machine.
const backendEnv = "DEDAO_SECRET_BACKEND"

const (
	BackendKeyring = "keyring"
	BackendFile    = "encrypted-file"
)

// Store holds secrets for one state directory.
type Store struct {
	dir string
}

func New(stateDir string) *Store { return &Store{dir: stateDir} }

// envelope is the on-disk format. The header is plaintext on purpose: knowing
// which backend sealed a file is what makes it openable, and it is not secret.
type envelope struct {
	Version int    `json:"v"`
	Backend string `json:"backend"`
	Salt    string `json:"salt,omitempty"`
	Nonce   string `json:"nonce"`
	Data    string `json:"data"`
}

func (s *Store) path(name string) string {
	return filepath.Join(s.dir, name+".enc")
}

// keyringUser namespaces the data key by state directory, so two state dirs on
// one machine do not fight over a single keyring entry.
func (s *Store) keyringUser(name string) string {
	sum := sha256.Sum256([]byte(s.dir))
	return fmt.Sprintf("%s-%s", name, base64.RawURLEncoding.EncodeToString(sum[:8]))
}

// keyringAvailable reports whether the OS keyring can actually hold a secret.
// It is probed by round-tripping a marker rather than by guessing from GOOS: a
// Linux desktop has a Secret Service and a Linux container does not.
func keyringAvailable() bool {
	if strings.EqualFold(os.Getenv(backendEnv), "file") {
		return false
	}
	const probe = "backend-probe"
	if err := keyring.Set(keyringService, probe, "1"); err != nil {
		return false
	}
	value, err := keyring.Get(keyringService, probe)
	_ = keyring.Delete(keyringService, probe)
	return err == nil && value == "1"
}

// Backend names the storage in use, for `context` and `doctor`.
func (s *Store) Backend() string {
	if keyringAvailable() {
		return BackendKeyring
	}
	return BackendFile
}

// StoredBackend reports the backend recorded in one sealed secret. The header
// is intentionally plaintext and contains no credential data.
func (s *Store) StoredBackend(name string) string {
	raw, err := os.ReadFile(s.path(name))
	if err != nil {
		return ""
	}
	var sealed envelope
	if err := json.Unmarshal(raw, &sealed); err != nil {
		return ""
	}
	if sealed.Backend == BackendKeyring || sealed.Backend == BackendFile {
		return sealed.Backend
	}
	return ""
}

// dataKey returns the 32-byte key for one secret, creating it if needed.
func (s *Store) dataKey(name string, backend string, salt []byte) ([]byte, error) {
	if backend == BackendKeyring {
		user := s.keyringUser(name)
		if encoded, err := keyring.Get(keyringService, user); err == nil {
			key, err := base64.StdEncoding.DecodeString(encoded)
			if err == nil && len(key) == 32 {
				return key, nil
			}
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := keyring.Set(keyringService, user, base64.StdEncoding.EncodeToString(key)); err != nil {
			return nil, err
		}
		return key, nil
	}
	return machineKey(salt)
}

// machineKey derives a key from factors that do not travel with the file.
//
// Hostname and username are enumerable by anyone already on this machine --
// that is the documented limit of this backend. What they are not is *present
// in the copied bytes*, so a state directory lifted onto another machine
// decrypts to nothing.
func machineKey(salt []byte) ([]byte, error) {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown-host"
	}
	name := "unknown-user"
	if current, err := user.Current(); err == nil {
		name = current.Uid + ":" + current.Username
	}
	material := strings.Join([]string{runtime.GOOS, host, name}, "\x00")
	return pbkdf2.Key(sha256.New, material, salt, 200_000, 32)
}

// Save seals data under name, replacing whatever was there.
func (s *Store) Save(name string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	backend := s.Backend()

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key, err := s.dataKey(name, backend, salt)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nil, nonce, data, []byte(backend))

	encoded, err := json.Marshal(envelope{
		Version: 1,
		Backend: backend,
		Salt:    base64.StdEncoding.EncodeToString(salt),
		Nonce:   base64.StdEncoding.EncodeToString(nonce),
		Data:    base64.StdEncoding.EncodeToString(sealed),
	})
	if err != nil {
		return err
	}
	// Written through a temp file so an interrupted write cannot leave a
	// truncated secret behind.
	temp := s.path(name) + ".tmp"
	if err := os.WriteFile(temp, encoded, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, s.path(name))
}

// ErrNotFound means nothing is stored under that name.
var ErrNotFound = errors.New("no stored secret")

// Load opens a sealed secret.
func (s *Store) Load(name string) ([]byte, error) {
	raw, err := os.ReadFile(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var sealed envelope
	if err := json.Unmarshal(raw, &sealed); err != nil {
		return nil, fmt.Errorf("the stored secret is not readable")
	}
	salt, err := base64.StdEncoding.DecodeString(sealed.Salt)
	if err != nil {
		return nil, fmt.Errorf("the stored secret is not readable")
	}
	key, err := s.dataKey(name, sealed.Backend, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.DecodeString(sealed.Nonce)
	if err != nil {
		return nil, fmt.Errorf("the stored secret is not readable")
	}
	payload, err := base64.StdEncoding.DecodeString(sealed.Data)
	if err != nil {
		return nil, fmt.Errorf("the stored secret is not readable")
	}
	opened, err := aead.Open(nil, nonce, payload, []byte(sealed.Backend))
	if err != nil {
		// Wrong machine, rotated keyring entry, or tampering -- all mean the
		// same thing to a caller: this session is unusable, log in again.
		return nil, fmt.Errorf("the stored secret could not be decrypted on this machine")
	}
	return opened, nil
}

// Delete removes a secret and any key the OS holds for it.
func (s *Store) Delete(name string) error {
	_ = keyring.Delete(keyringService, s.keyringUser(name))
	if err := os.Remove(s.path(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Has reports whether a sealed secret exists, without opening it.
func (s *Store) Has(name string) bool {
	_, err := os.Stat(s.path(name))
	return err == nil
}

// AdoptPlaintext migrates a legacy plaintext file into the store and removes
// it.
//
// Earlier builds wrote the session as plaintext JSON. Leaving those files in
// place would make the encryption cosmetic, so the first run that finds one
// seals it and deletes the original.
func (s *Store) AdoptPlaintext(name, legacyPath string) ([]byte, bool) {
	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		return nil, false
	}
	if err := s.Save(name, raw); err != nil {
		// Migration failed; leave the legacy file alone rather than losing the
		// session, and let the caller use it this run.
		return raw, true
	}
	_ = os.Remove(legacyPath)
	return raw, true
}
