package secret

import (
	"os"
	"testing"
)

// TestMain keeps the suite off the developer's real credential store; the
// keyring path is covered by an explicit opt-in test instead.
func TestMain(m *testing.M) {
	os.Setenv("DEDAO_SECRET_BACKEND", "file")
	os.Exit(m.Run())
}
