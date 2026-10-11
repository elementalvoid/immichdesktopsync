package immich

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"immich-desktop-sync/backend/models"
)

func sha256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writePatternFile(t *testing.T, path string, size int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 32*1024)
	for i := range buf {
		buf[i] = byte(i)
	}
	for written := 0; written < size; {
		n := min(len(buf), size-written)
		if _, err := f.Write(buf[:n]); err != nil {
			t.Fatal(err)
		}
		written += n
	}
}

// TestUploadFileSendsFileAndFields proves the streamed multipart body still
// carries the file bytes and the metadata fields the Immich API expects.
func TestUploadFileSendsFileAndFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")
	writePatternFile(t, path, 3<<20)
	wantHash := sha256File(t, path)

	type upload struct {
		hash     string
		filename string
		fields   map[string]string
	}
	got := make(chan upload, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/assets" {
			t.Errorf("request = %s %s, want POST /api/assets", r.Method, r.URL.Path)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// The body must carry a Content-Length, not chunked encoding, and the
		// declared length must match the bytes actually sent.
		if len(r.TransferEncoding) != 0 {
			t.Errorf("Transfer-Encoding = %v, want none (Content-Length request)", r.TransferEncoding)
		}
		if r.ContentLength <= 0 {
			t.Errorf("Content-Length = %d, want > 0", r.ContentLength)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if int64(len(raw)) != r.ContentLength {
			t.Errorf("body = %d bytes, Content-Length = %d", len(raw), r.ContentLength)
		}
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Errorf("parse content type: %v", err)
			return
		}
		mr := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
		u := upload{fields: map[string]string{}}
		h := sha256.New()
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("next part: %v", err)
				return
			}
			if part.FormName() == "assetData" {
				u.filename = part.FileName()
				if _, err := io.Copy(h, part); err != nil {
					t.Errorf("read file part: %v", err)
					return
				}
				continue
			}
			b, err := io.ReadAll(part)
			if err != nil {
				t.Errorf("read field %s: %v", part.FormName(), err)
				return
			}
			u.fields[part.FormName()] = string(b)
		}
		u.hash = hex.EncodeToString(h.Sum(nil))
		got <- u
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"asset-123"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	id, err := c.UploadFile(path)
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if id != "asset-123" {
		t.Errorf("asset id = %q, want asset-123", id)
	}

	u := <-got
	if u.hash != wantHash {
		t.Errorf("uploaded bytes hash = %s, want %s", u.hash, wantHash)
	}
	if u.filename != "photo.jpg" {
		t.Errorf("filename = %q, want photo.jpg", u.filename)
	}
	if want := "photo.jpg-" + strconv.Itoa(3<<20); u.fields["deviceAssetId"] != want {
		t.Errorf("deviceAssetId = %q, want %q", u.fields["deviceAssetId"], want)
	}
	if u.fields["deviceId"] != "immich-desktop-sync" {
		t.Errorf("deviceId = %q, want immich-desktop-sync", u.fields["deviceId"])
	}
	if u.fields["isFavorite"] != "false" {
		t.Errorf("isFavorite = %q, want false", u.fields["isFavorite"])
	}
}

// TestUploadFileDoesNotBufferWholeFile is the regression guard for the memory
// blow-up: the body must stream, so the heap must not grow by the file size.
// The old code copied the file into a bytes.Buffer first.
func TestUploadFileDoesNotBufferWholeFile(t *testing.T) {
	const size = 64 << 20
	path := filepath.Join(t.TempDir(), "big.bin")
	writePatternFile(t, path, size)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, part)
		}
		_, _ = w.Write([]byte(`{"id":"asset-123"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := c.UploadFile(path); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	runtime.ReadMemStats(&after)

	if growth := int64(after.HeapAlloc) - int64(before.HeapAlloc); growth > size/4 {
		t.Fatalf("heap grew by %d bytes during a %d-byte upload; the file was buffered", growth, size)
	}
}

// TestSearchMetadataPagePaginates checks that one call fetches one page and
// that the server hasNext flag reaches the caller.
func TestSearchMetadataPagePaginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Page int `json:"page"`
			Size int `json:"size"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.Size != 2 {
			t.Errorf("size = %d, want 2", req.Size)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"assets":{"total":4,"count":2,"page":%d,"pages":2,"hasNext":%t,`+
			`"items":[{"id":"p%d-a"},{"id":"p%d-b"}]}}`, req.Page, req.Page < 2, req.Page, req.Page)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")

	items, hasNext, err := c.SearchMetadataPage(t.Context(), 1, 2, models.SearchRequest{})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if !hasNext {
		t.Error("page 1: hasNext = false, want true")
	}
	if len(items) != 2 || items[0].ID != "p1-a" || items[1].ID != "p1-b" {
		t.Errorf("page 1 items = %+v, want p1-a and p1-b", items)
	}

	items, hasNext, err = c.SearchMetadataPage(t.Context(), 2, 2, models.SearchRequest{})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if hasNext {
		t.Error("page 2: hasNext = true, want false")
	}
	if len(items) != 2 || items[0].ID != "p2-a" {
		t.Errorf("page 2 items = %+v, want p2-a and p2-b", items)
	}
}

// TestSearchMetadataPageHonorsContext proves that cancelling the caller's
// context aborts an in-flight request, which is how a logout stops a fetch.
func TestSearchMetadataPageHonorsContext(t *testing.T) {
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

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		<-started
		cancel()
	}()

	c := NewClient(srv.URL, "token")
	if _, _, err := c.SearchMetadataPage(ctx, 1, 2, models.SearchRequest{}); err == nil {
		t.Fatal("SearchMetadataPage returned a nil error after the context was cancelled")
	}
}
