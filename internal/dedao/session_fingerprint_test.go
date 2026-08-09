package dedao

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialFingerprintIsStableAndCredentialBound(t *testing.T) {
	clientWith := func(cookies ...*http.Cookie) *Client {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		base, _ := url.Parse(BaseURL + "/")
		jar.SetCookies(base, cookies)
		return &Client{jar: jar, baseURL: BaseURL}
	}

	first := clientWith(
		&http.Cookie{Name: "b", Value: "2"},
		&http.Cookie{Name: "a", Value: "1"},
	).CredentialFingerprint()
	same := clientWith(
		&http.Cookie{Name: "a", Value: "1"},
		&http.Cookie{Name: "b", Value: "2"},
	).CredentialFingerprint()
	changed := clientWith(
		&http.Cookie{Name: "a", Value: "different"},
		&http.Cookie{Name: "b", Value: "2"},
	).CredentialFingerprint()

	if first == "" || first != same {
		t.Fatalf("same credential fingerprint changed: %q != %q", first, same)
	}
	if first == changed {
		t.Fatalf("different credentials share fingerprint %q", first)
	}
}

func TestCredentialFingerprintIncludesLegacyPlaintext(t *testing.T) {
	dir := t.TempDir()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{jar: jar, baseURL: BaseURL, stateDir: dir}
	withoutLegacy := client.CredentialFingerprint()

	path := filepath.Join(dir, "cookies.json")
	if err := os.WriteFile(path, []byte("first-session"), 0o600); err != nil {
		t.Fatal(err)
	}
	withLegacy := client.CredentialFingerprint()
	if withLegacy == withoutLegacy || withLegacy != client.CredentialFingerprint() {
		t.Fatal("legacy credential fingerprint is not present and stable")
	}

	if err := os.WriteFile(path, []byte("second-session"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed := client.CredentialFingerprint(); changed == withLegacy {
		t.Fatal("replaced legacy credential did not change fingerprint")
	}
}
