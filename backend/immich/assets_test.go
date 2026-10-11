package immich

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
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
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("multipart reader: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
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
