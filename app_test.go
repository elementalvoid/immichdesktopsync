package main

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"immich-desktop-sync/backend/immich"
	"immich-desktop-sync/backend/models"
)

func newTestApp(serverURL, downloads string) *App {
	return &App{
		cfg:    &models.Config{DownloadsFolder: downloads},
		client: immich.NewClient(serverURL, "token"),
	}
}

// TestDownloadAssetWritesWhileStreaming is the regression guard for the memory
// blow-up: DownloadAsset must write each chunk to disk as it arrives. The old
// code read the whole original with io.ReadAll, so the destination file stayed
// empty until the response completed.
func TestDownloadAssetWritesWhileStreaming(t *testing.T) {
	const total = 8 << 20
	half := total / 2
	payload := make([]byte, total)
	for i := range payload {
		payload[i] = byte(i)
	}

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(total))
		_, _ = w.Write(payload[:half])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
		_, _ = w.Write(payload[half:])
	}))
	defer srv.Close()

	dir := t.TempDir()
	app := newTestApp(srv.URL, dir)

	errCh := make(chan error, 1)
	go func() { errCh <- app.DownloadAsset("abc", "out.bin") }()

	dest := filepath.Join(dir, "out.bin")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if fi, err := os.Stat(dest); err == nil && fi.Size() >= int64(half) {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			<-errCh
			t.Fatal("destination stayed empty while the response streamed; the body was buffered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("DownloadAsset: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if sum, want := sha256.Sum256(got), sha256.Sum256(payload); sum != want {
		t.Errorf("downloaded bytes differ from the source")
	}
}

// TestDownloadAssetRemovesPartialFileOnError proves a truncated download does
// not leave a file that looks complete.
func TestDownloadAssetRemovesPartialFileOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(1<<20))
		_, _ = w.Write(make([]byte, 1024))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()

	dir := t.TempDir()
	app := newTestApp(srv.URL, dir)
	if err := app.DownloadAsset("abc", "out.bin"); err == nil {
		t.Fatal("DownloadAsset succeeded on a truncated response, want an error")
	}
	if _, err := os.Stat(filepath.Join(dir, "out.bin")); !os.IsNotExist(err) {
		t.Fatalf("partial file kept after a failed download (stat err = %v)", err)
	}
}

// TestDownloadAssetKeepsExistingFile guards the collision rename: an existing
// file must not be overwritten.
func TestDownloadAssetKeepsExistingFile(t *testing.T) {
	payload := []byte("new bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	app := newTestApp(srv.URL, dir)
	if err := app.DownloadAsset("abc", "photo.jpg"); err != nil {
		t.Fatal(err)
	}

	if orig, _ := os.ReadFile(filepath.Join(dir, "photo.jpg")); string(orig) != "original" {
		t.Errorf("existing file was overwritten: %q", orig)
	}
	got, err := os.ReadFile(filepath.Join(dir, "photo (1).jpg"))
	if err != nil {
		t.Fatalf("renamed download missing: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("renamed download = %q, want %q", got, payload)
	}
}

// TestCancelFetchStopsInFlightRequest proves the App-owned fetch context is the
// one a logout cancels (Logout calls cancelFetch). A blocked request must
// return an error as soon as cancelFetch runs.
func TestCancelFetchStopsInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	app := &App{client: immich.NewClient(srv.URL, "token")}
	errCh := make(chan error, 1)
	go func() {
		_, _, err := app.client.SearchMetadataPage(app.fetchContext(), 1, 10, models.SearchRequest{})
		errCh <- err
	}()

	<-started
	app.cancelFetch()
	if err := <-errCh; err == nil {
		t.Fatal("in-flight request survived cancelFetch")
	}
}
