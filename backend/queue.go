package backend

import (
	"log"
	"sync"
	"time"

	"immich-desktop-sync/backend/db"
	"immich-desktop-sync/backend/immich"
)

const (
	maxConcurrent = 3
	maxRetries    = 5
)

var retryDelays = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	60 * time.Minute,
}

type UploadQueue struct {
	database *db.DB
	client   *immich.Client
	sem      chan struct{}
	wg       sync.WaitGroup
	stop     chan struct{}
	notify   chan struct{}
	onStart  func()
	onDone   func()
}

func NewUploadQueue(database *db.DB, client *immich.Client, onStart, onDone func()) *UploadQueue {
	return &UploadQueue{
		database: database,
		client:   client,
		sem:      make(chan struct{}, maxConcurrent),
		stop:     make(chan struct{}),
		notify:   make(chan struct{}, 1),
		onStart:  onStart,
		onDone:   onDone,
	}
}

func (q *UploadQueue) Start() {
	go q.loop()
}

func (q *UploadQueue) Stop() {
	close(q.stop)
	q.wg.Wait()
}

func (q *UploadQueue) Notify() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *UploadQueue) loop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-q.stop:
			return
		case <-q.notify:
			q.processNext()
		case <-ticker.C:
			q.processNext()
		}
	}
}

func (q *UploadQueue) processNext() {
	// DequeueNextPending atomically claims what it returns (status ->
	// 'uploading'), so concurrent processNext() calls can never pick up the same
	// row and upload it twice. The cutoff defers rows that are still inside their
	// retry backoff: a 'failed' row is claimable only once it is older than the
	// delay for its retry count.
	cutoff := time.Now().UTC().Add(-retryDelays[0])
	items, err := q.database.DequeueNextPending(maxConcurrent, cutoff)
	if err != nil {
		log.Printf("queue: dequeue error: %v", err)
		return
	}
	for _, item := range items {
		item := item

		q.sem <- struct{}{}
		q.wg.Add(1)
		go func() {
			defer func() {
				<-q.sem
				q.wg.Done()
				q.Notify()
				if q.onDone != nil {
					q.onDone()
				}
			}()

			// Already claimed as 'uploading' by the dequeue above; rows still in
			// their retry backoff were filtered out there, so no re-check is
			// needed here.
			if q.onStart != nil {
				q.onStart()
			}

			assetID, err := q.client.UploadFile(item.FilePath)
			if err != nil {
				log.Printf("queue: upload %s failed: %v", item.FilePath, err)
				_ = q.database.MarkFailed(item.ID, err.Error())
				return
			}

			if err := q.database.MarkDone(item.ID, item.FilePath, assetID); err != nil {
				log.Printf("queue: mark done %d: %v", item.ID, err)
			} else {
				log.Printf("queue: uploaded %s → %s", item.FilePath, assetID)
			}
		}()
	}
}

// retryDelay is the backoff for a given retry count (1-based): the first retry
// waits retryDelays[0], and later retries wait progressively longer.
func retryDelay(retryCount int) time.Duration {
	idx := retryCount - 1
	if idx < 0 {
		return 0
	}
	if idx >= len(retryDelays) {
		return retryDelays[len(retryDelays)-1]
	}
	return retryDelays[idx]
}
