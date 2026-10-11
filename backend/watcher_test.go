package backend

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"immich-desktop-sync/backend/db"
)

func newTestWatcher(t *testing.T) (*FolderWatcher, *db.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	fw, err := NewFolderWatcher(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fw.Close() })
	fw.Start()
	return fw, d
}

func queued(t *testing.T, d *db.DB, base string) bool {
	t.Helper()
	items, err := d.GetQueue()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if filepath.Base(it.FilePath) == base {
			return true
		}
	}
	return false
}

// waitQueued fails unless base appears in the queue within the deadline.
func waitQueued(t *testing.T, d *db.DB, base string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if queued(t, d, base) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("file %s was never queued", base)
}

// assertNotQueued fails if base appears in the queue during the window. The
// window must outlast the delay between an fsnotify event and the enqueue.
func assertNotQueued(t *testing.T, d *db.DB, base string, window time.Duration) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		if queued(t, d, base) {
			t.Fatalf("file %s was queued after the folder was unwatched", base)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRemoveFolderUnwatchesNestedDirs guards the watcher leak: AddFolder walks
// a root and watches every subdirectory, but RemoveFolder used to remove only
// the root, leaving a watch on each subdirectory.
func TestRemoveFolderUnwatchesNestedDirs(t *testing.T) {
	fw, d := newTestWatcher(t)
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := fw.AddFolder(root); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(sub, "before.jpg"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	waitQueued(t, d, "before.jpg")

	if err := fw.RemoveFolder(root); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(sub, "after.jpg"), []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	assertNotQueued(t, d, "after.jpg", 700*time.Millisecond)
}

// TestRemoveFolderUnwatchesAutoAddedSubdir guards the same leak for a directory
// created after AddFolder, which loop() watches on its own.
func TestRemoveFolderUnwatchesAutoAddedSubdir(t *testing.T) {
	fw, d := newTestWatcher(t)
	root := t.TempDir()
	if err := fw.AddFolder(root); err != nil {
		t.Fatal(err)
	}

	// loop() watches a new directory asynchronously, so write until an event
	// proves the watch exists.
	sub := filepath.Join(root, "later")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !queued(t, d, "before.mp4") {
		if time.Now().After(deadline) {
			t.Fatal("the auto-added subdirectory was never watched")
		}
		if err := os.WriteFile(filepath.Join(sub, "before.mp4"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := fw.RemoveFolder(root); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(sub, "after.mp4"), []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	assertNotQueued(t, d, "after.mp4", 700*time.Millisecond)
}

// TestRemoveFolderStillEnqueuesNestedDirsBeforeRemove pins the positive case
// the two leak tests rely on: a nested directory is watched before removal.
func TestNestedDirIsWatched(t *testing.T) {
	fw, d := newTestWatcher(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := fw.AddFolder(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b", "deep.jpg"), []byte("z"), 0644); err != nil {
		t.Fatal(err)
	}
	waitQueued(t, d, "deep.jpg")
}
