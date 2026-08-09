package update

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testReleaseWithAssets(t *testing.T, baseURL string, names ...string) *Release {
	t.Helper()
	assets := make([]map[string]string, 0, len(names))
	for _, name := range names {
		assets = append(assets, map[string]string{
			"name":                 name,
			"browser_download_url": baseURL + "/" + name,
		})
	}
	raw, err := json.Marshal(map[string]any{"tag_name": "v1.0.0", "assets": assets})
	if err != nil {
		t.Fatal(err)
	}
	var release Release
	if err := json.Unmarshal(raw, &release); err != nil {
		t.Fatal(err)
	}
	return &release
}

func TestDownloadAsset_MissingIsDistinctFromTransportFailure(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	release := testReleaseWithAssets(t, server.URL, "missing.tar.gz")
	_, err := downloadAsset(context.Background(), server.Client(), release, "missing.tar.gz")
	if !errors.Is(err, ErrAssetMissing) {
		t.Fatalf("error = %v, want ErrAssetMissing", err)
	}
}

func TestInstallBinary_DoesNotTurnSignatureTransportIntoIntegrity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checksums.txt" {
			_, _ = w.Write([]byte("abc  x.tar.gz\n"))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	release := testReleaseWithAssets(t, server.URL, "checksums.txt", "checksums.txt.sigstore.json")
	config := Config{Tool: "dedao-cli", Repo: "owner/repo"}
	_, err := func() (*Result, error) {
		result := &Result{}
		return result, config.installBinary(context.Background(), server.Client(), release, result)
	}()
	if err == nil {
		t.Fatal("signature transport failure returned success")
	}
	if errors.Is(err, ErrIntegrity) {
		t.Fatalf("signature transport failure was misclassified as integrity: %v", err)
	}
}

func TestInstallBinary_MissingChecksumsIsIntegrityFailure(t *testing.T) {
	config := Config{Tool: "dedao-cli", Repo: "owner/repo"}
	result := &Result{}
	err := config.installBinary(context.Background(), http.DefaultClient, &Release{Version: "1.0.0"}, result)
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("missing checksums = %v, want ErrIntegrity", err)
	}
}
