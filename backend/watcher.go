package backend

import (
	"log"
	"os"
	"path/filepath"
	"strings"
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
}

func NewFolderWatcher(database *db.DB, onEnqueue func()) (*FolderWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &FolderWatcher{watcher: w, database: database, onEnqueue: onEnqueue}, nil
}

func (fw *FolderWatcher) AddFolder(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("watcher: walk %s: %v", path, err)
			return nil
		}
		if info.IsDir() {
			if watchErr := fw.watcher.Add(path); watchErr != nil {
				log.Printf("watcher: add %s: %v", path, watchErr)
			}
		}
		return nil
	})
}

func (fw *FolderWatcher) RemoveFolder(path string) error {
	return fw.watcher.Remove(path)
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
					if err := fw.watcher.Add(path); err != nil {
						log.Printf("watcher: auto-add dir %s: %v", path, err)
					} else {
						log.Printf("watcher: watching new dir %s", path)
					}
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
