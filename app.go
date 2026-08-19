package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"immich-desktop-sync/backend"
	"immich-desktop-sync/backend/db"
	"immich-desktop-sync/backend/immich"
	"immich-desktop-sync/backend/models"
)

type App struct {
	app      *application.App
	cfg      *models.Config
	db       *db.DB
	client   *immich.Client
	auth     *backend.AuthManager
	queue    *backend.UploadQueue
	watcher  *backend.FolderWatcher
	proxy    *backend.StreamProxy
	thumbSem chan struct{}
}

func NewApp(app *application.App) *App {
	return &App{app: app, thumbSem: make(chan struct{}, 8)}
}

func (a *App) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	cfg, err := backend.LoadConfig()
	if err != nil {
		log.Printf("startup: load config: %v", err)
		cfg = &models.Config{}
	}
	a.cfg = cfg

	if err := os.MkdirAll(filepath.Dir(backend.DBPath()), 0700); err != nil {
		log.Printf("startup: mkdir: %v", err)
	}
	database, err := db.Open(backend.DBPath())
	if err != nil {
		log.Printf("startup: open db: %v", err)
	} else {
		a.db = database
		if err := database.ResetStuckUploads(); err != nil {
			log.Printf("startup: reset stuck uploads: %v", err)
		}
	}

	a.client = immich.NewClient(cfg.ServerURL, cfg.AccessToken)

	a.auth = backend.NewAuthManager(cfg, a.client)
	a.auth.RestoreSession()

	if proxy, err := backend.NewStreamProxy(a.client); err != nil {
		log.Printf("startup: stream proxy: %v", err)
	} else {
		a.proxy = proxy
	}

	if a.db != nil {
		a.queue = backend.NewUploadQueue(a.db, a.client,
			func() { a.app.Event.Emit("upload:started") },
			func() { a.app.Event.Emit("upload:done") },
		)
		a.queue.Start()
	}

	if a.db != nil {
		var onEnqueue func()
		if a.queue != nil {
			onEnqueue = a.queue.Notify
		}
		fw, err := backend.NewFolderWatcher(a.db, onEnqueue)
		if err != nil {
			log.Printf("startup: folder watcher: %v", err)
		} else {
			a.watcher = fw
			folders, _ := a.db.GetFolders()
			for _, f := range folders {
				if err := fw.AddFolder(f); err != nil {
					log.Printf("startup: re-watch %s: %v", f, err)
				}
			}
			fw.Start()
		}
	}
	return nil
}

// handleFileDrop enqueues dropped files and notifies the UI.
func (a *App) handleFileDrop(paths []string) {
	if a.db == nil {
		return
	}
	count := 0
	for _, p := range paths {
		if !backend.IsMediaFile(p) {
			continue
		}
		uploaded, err := a.db.IsUploaded(p)
		if err != nil {
			log.Printf("OnFileDrop: check uploaded %s: %v", p, err)
		}
		if uploaded {
			continue
		}
		if err := a.db.EnqueueFile(p); err != nil {
			log.Printf("OnFileDrop: enqueue %s: %v", p, err)
		} else {
			count++
		}
	}
	if count > 0 && a.queue != nil {
		a.queue.Notify()
	}
	a.app.Event.Emit("files:dropped", count)
}

func (a *App) shutdown() {
	if a.proxy != nil {
		a.proxy.Close()
	}
	if a.watcher != nil {
		_ = a.watcher.Close()
	}
	if a.queue != nil {
		a.queue.Stop()
	}
	if a.db != nil {
		_ = a.db.Close()
	}
}

func (a *App) Login(serverURL, email, password string) (*models.User, error) {
	user, err := a.auth.Login(serverURL, email, password)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (a *App) Logout() error {
	return a.auth.Logout()
}

func (a *App) IsAuthenticated() bool {
	return a.auth.IsAuthenticated()
}

func (a *App) GetServerURL() string {
	return a.cfg.ServerURL
}

func (a *App) GetAssets() ([]models.Asset, error) {
	if !a.auth.IsAuthenticated() {
		return nil, fmt.Errorf("not authenticated")
	}
	return a.client.SearchMetadata(models.SearchRequest{})
}

func (a *App) GetServerVersion() (string, error) {
	return a.client.GetServerVersion()
}

func (a *App) GetAlbums() ([]models.Album, error) {
	if !a.auth.IsAuthenticated() {
		return nil, fmt.Errorf("not authenticated")
	}
	return a.client.GetAlbums()
}

func (a *App) GetAlbumAssets(albumID string) ([]models.Asset, error) {
	if !a.auth.IsAuthenticated() {
		return nil, fmt.Errorf("not authenticated")
	}
	return a.client.GetAlbumAssets(albumID)
}

func (a *App) GetAccessToken() string {
	return a.cfg.AccessToken
}

func (a *App) GetAssetInfo(assetID string) (*models.Asset, error) {
	if !a.auth.IsAuthenticated() {
		return nil, fmt.Errorf("not authenticated")
	}
	return a.client.GetAssetInfo(assetID)
}

func (a *App) GetStreamPort() int {
	if a.proxy == nil {
		return 0
	}
	return a.proxy.Port()
}

func (a *App) GetThumbnail(assetID, size string) ([]byte, error) {
	if size == "" {
		size = "thumb"
	}
	key := assetID + ":" + size

	if a.db != nil {
		if cached, err := a.db.GetThumbnail(key); err == nil && cached != nil {
			return cached, nil
		}
	}

	a.thumbSem <- struct{}{}
	defer func() { <-a.thumbSem }()

	if a.db != nil {
		if cached, err := a.db.GetThumbnail(key); err == nil && cached != nil {
			return cached, nil
		}
	}

	data, err := a.client.FetchThumbnail(assetID, size)
	if err != nil {
		return nil, err
	}
	if a.db != nil {
		_ = a.db.CacheThumbnail(key, data)
	}
	return data, nil
}

func (a *App) GetFolders() ([]string, error) {
	if a.db == nil {
		return a.cfg.Folders, nil
	}
	return a.db.GetFolders()
}

func (a *App) AddFolder(path string) error {
	if a.db != nil {
		if err := a.db.AddFolder(path); err != nil {
			return err
		}
	}
	if a.watcher != nil {
		if err := a.watcher.AddFolder(path); err != nil {
			log.Printf("AddFolder: watch %s: %v", path, err)
		}
	}
	if a.db != nil {
		go a.scanFolder(path)
	}
	return nil
}

func (a *App) RemoveFolder(path string) error {
	if a.db != nil {
		if err := a.db.RemoveFolder(path); err != nil {
			return err
		}
	}
	if a.watcher != nil {
		return a.watcher.RemoveFolder(path)
	}
	return nil
}

func (a *App) SelectFolder() (string, error) {
	return a.app.Dialog.OpenFile().
		SetTitle("Select folder to sync").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()
}

func (a *App) GetUploadQueue() ([]models.UploadQueueItem, error) {
	if a.db == nil {
		return nil, nil
	}
	return a.db.GetQueue()
}

func (a *App) RetryFailed() error {
	if a.db == nil {
		return nil
	}
	if err := a.db.ResetFailedUploads(); err != nil {
		return err
	}
	if a.queue != nil {
		a.queue.Notify()
	}
	return nil
}

func (a *App) GetDownloadsFolder() string {
	return a.cfg.DownloadsFolder
}

func (a *App) SetDownloadsFolder(path string) error {
	a.cfg.DownloadsFolder = path
	return backend.SaveConfig(a.cfg)
}

func (a *App) DownloadAsset(assetID, filename string) error {
	if a.cfg.DownloadsFolder == "" {
		return fmt.Errorf("no downloads folder set — configure one in Settings")
	}
	if err := os.MkdirAll(a.cfg.DownloadsFolder, 0755); err != nil {
		return fmt.Errorf("create downloads folder: %w", err)
	}

	data, err := a.client.GetOriginal(assetID)
	if err != nil {
		return fmt.Errorf("fetch asset: %w", err)
	}

	dest := filepath.Join(a.cfg.DownloadsFolder, filename)
	if _, err := os.Stat(dest); err == nil {
		ext := filepath.Ext(filename)
		base := filename[:len(filename)-len(ext)]
		for i := 1; ; i++ {
			dest = filepath.Join(a.cfg.DownloadsFolder, fmt.Sprintf("%s (%d)%s", base, i, ext))
			if _, err := os.Stat(dest); os.IsNotExist(err) {
				break
			}
		}
	}

	if err := os.WriteFile(dest, data, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	log.Printf("downloaded %s → %s", assetID, dest)
	return nil
}

func (a *App) ClearCache() error {
	if a.db == nil {
		return nil
	}
	return a.db.ClearCache()
}

func (a *App) UploadFiles(paths []string) error {
	if a.db == nil {
		return fmt.Errorf("database not ready")
	}
	count := 0
	for _, path := range paths {
		if !backend.IsMediaFile(path) {
			continue
		}
		uploaded, err := a.db.IsUploaded(path)
		if err != nil {
			log.Printf("UploadFiles: check uploaded %s: %v", path, err)
		}
		if uploaded {
			continue
		}
		if err := a.db.EnqueueFile(path); err != nil {
			log.Printf("UploadFiles: enqueue %s: %v", path, err)
		} else {
			count++
		}
	}
	if count > 0 && a.queue != nil {
		a.queue.Notify()
	}
	return nil
}

func (a *App) scanFolder(root string) {
	count := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("scanFolder walk %s: %v", path, err)
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !backend.IsMediaFile(path) {
			return nil
		}
		if uploaded, _ := a.db.IsUploaded(path); uploaded {
			return nil
		}
		if enqErr := a.db.EnqueueFile(path); enqErr == nil {
			count++
			if count%10 == 0 && a.queue != nil {
				a.queue.Notify()
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("scanFolder %s: %v", root, err)
	}
	log.Printf("scanFolder %s: enqueued %d files", root, count)
	if count > 0 && a.queue != nil {
		a.queue.Notify()
	}
}
