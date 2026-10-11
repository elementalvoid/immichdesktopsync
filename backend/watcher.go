package backend

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"immich-desktop-sync/backend/db"
)

var mediaExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".webp": true,
	".heic": true,
	".heif": true,
	".tiff": true,
	".tif":  true,
	".mp4":  true,
	".mov":  true,
	".avi":  true,
	".mkv":  true,
	".3gp":  true,
	".wmv":  true,
}

func IsMediaFile(path string) bool {
	return mediaExtensions[strings.ToLower(filepath.Ext(path))]
}

type FolderWatcher struct {
	watcher   *fsnotify.Watcher
	database  *db.DB
	onEnqueue func()

	// watched maps a watched root to every directory watched for it, so
	// RemoveFolder can remove the nested watches AddFolder and loop() created.
	// The loop() goroutine and the UI goroutine both touch it.
	mu      sync.Mutex
	watched map[string]map[string]bool
}

func NewFolderWatcher(database *db.DB, onEnqueue func()) (*FolderWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &FolderWatcher{
		watcher:   w,
		database:  database,
		onEnqueue: onEnqueue,
		watched:   make(map[string]map[string]bool),
	}, nil
}

func (fw *FolderWatcher) AddFolder(root string) error {
	root = filepath.Clean(root)
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("watcher: walk %s: %v", path, err)
			return nil
		}
		if info.IsDir() {
			fw.addWatch(root, path)
		}
		return nil
	})
}

// addWatch registers dir with fsnotify and records it under root. It records
// only a watch that was added, so RemoveFolder never tries to drop a watch that
// does not exist.
func (fw *FolderWatcher) addWatch(root, dir string) {
	if err := fw.watcher.Add(dir); err != nil {
		log.Printf("watcher: add %s: %v", dir, err)
		return
	}
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.watched[root] == nil {
		fw.watched[root] = make(map[string]bool)
	}
	fw.watched[root][dir] = true
}

// watchNewDir finds the owning root and records the watch under one lock, so a
// concurrent RemoveFolder cannot delete the root between finding it and adding
// the watch (which would leave a stale watch after removal).
func (fw *FolderWatcher) watchNewDir(dir string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	root := fw.rootForLocked(dir)
	if root == "" {
		return
	}
	if err := fw.watcher.Add(dir); err != nil {
		log.Printf("watcher: add %s: %v", dir, err)
		return
	}
	if fw.watched[root] == nil {
		fw.watched[root] = make(map[string]bool)
	}
	fw.watched[root][dir] = true
	log.Printf("watcher: watching new dir %s", dir)
}

// rootForLocked returns the watched root that contains dir, preferring the
// longest match so that nested roots do not claim each other's directories. The
// caller must hold fw.mu.
func (fw *FolderWatcher) rootForLocked(dir string) string {
	best := ""
	for root := range fw.watched {
		if dir == root || strings.HasPrefix(dir, root+string(os.PathSeparator)) {
			if len(root) > len(best) {
				best = root
			}
		}
	}
	return best
}

func (fw *FolderWatcher) RemoveFolder(root string) error {
	root = filepath.Clean(root)
	fw.mu.Lock()
	dirs := fw.watched[root]
	delete(fw.watched, root)
	// A directory also recorded under another root (a nested folder added
	// separately) must keep its watch: remove only what this root alone owns.
	keep := make(map[string]bool)
	for other, set := range fw.watched {
		if other == root {
			continue
		}
		for dir := range set {
			keep[dir] = true
		}
	}
	fw.mu.Unlock()

	if dirs == nil {
		// No recorded root: fall back to removing the path itself.
		return fw.watcher.Remove(root)
	}
	var firstErr error
	for dir := range dirs {
		if keep[dir] {
			continue
		}
		if err := fw.watcher.Remove(dir); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (fw *FolderWatcher) Start() {
	go fw.loop()
}

func (fw *FolderWatcher) Close() error {
	return fw.watcher.Close()
}

func (fw *FolderWatcher) loop() {
	seen := make(map[string]time.Time)

	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}

			path := event.Name

			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					fw.watchNewDir(path)
					continue
				}
			}

			if event.Op&(fsnotify.Create|fsnotify.Write) == 0 {
				continue
			}
			if !IsMediaFile(path) {
				continue
			}
			if last, ok := seen[path]; ok && time.Since(last) < 2*time.Second {
				continue
			}
			seen[path] = time.Now()

			if err := fw.database.EnqueueFile(path); err != nil {
				log.Printf("watcher: enqueue %s: %v", path, err)
			} else {
				log.Printf("watcher: enqueued %s", path)
				if fw.onEnqueue != nil {
					fw.onEnqueue()
				}
			}

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("watcher error: %v", err)
		}
	}
}
