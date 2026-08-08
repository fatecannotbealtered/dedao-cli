package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A repository with no published release answers 404. That is a definite
// answer, not a failure to get one, and it must not be reported as a retryable
// network problem -- an agent would retry until someone tags a release.
func TestFetchLatest_NoReleaseIsNotANetworkFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := Config{Tool: "tool", Version: "0.1.0", Repo: "owner/repo"}
	_, err := config.fetchLatestFrom(context.Background(), server.Client(), server.URL)
	if !errors.Is(err, ErrNoRelease) {
		t.Fatalf("err = %v, want ErrNoRelease", err)
	}
}

// Any other refusal is still an error worth surfacing as one.
func TestFetchLatest_OtherFailuresStaySeparate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	config := Config{Tool: "tool", Version: "0.1.0", Repo: "owner/repo"}
	_, err := config.fetchLatestFrom(context.Background(), server.Client(), server.URL)
	if err == nil || errors.Is(err, ErrNoRelease) {
		t.Fatalf("err = %v, want a plain failure", err)
	}
}
