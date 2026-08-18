package models

import (
	"encoding/json"
	"fmt"
)

type ExifInfo struct {
	FileSizeInByte  int64    `json:"fileSizeInByte"`
	ExifImageWidth  int      `json:"exifImageWidth"`
	ExifImageHeight int      `json:"exifImageHeight"`
	Make            string   `json:"make"`
	Model           string   `json:"model"`
	LensModel       string   `json:"lensModel"`
	FNumber         float64  `json:"fNumber"`
	FocalLength     float64  `json:"focalLength"`
	Iso             int      `json:"iso"`
	ExposureTime    string   `json:"exposureTime"`
	Latitude        *float64 `json:"latitude"`
	Longitude       *float64 `json:"longitude"`
	City            string   `json:"city"`
	State           string   `json:"state"`
	Country         string   `json:"country"`
	Description     string   `json:"description"`
}

type Asset struct {
	ID               string    `json:"id"`
	OriginalPath     string    `json:"originalPath"`
	OriginalFileName string    `json:"originalFileName"`
	Checksum         string    `json:"checksum"`
	Type             string    `json:"type"`
	FileCreatedAt    string    `json:"fileCreatedAt"`
	FileModifiedAt   string    `json:"fileModifiedAt"`
	LocalDateTime    string    `json:"localDateTime"`
	Duration         Duration  `json:"duration"`
	IsFavorite       bool      `json:"isFavorite"`
	CreatedAt        string    `json:"createdAt"`
	UpdatedAt        string    `json:"updatedAt"`
	ThumbURL         string    `json:"thumbUrl,omitempty"`
	ExifInfo         *ExifInfo `json:"exifInfo,omitempty"`
}

// Duration accepts either Immich's "H:MM:SS" string or a numeric seconds
// value (some server versions return a number) and normalizes to a string.
type Duration string

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*d = Duration(s)
		return nil
	}
	var sec float64
	if err := json.Unmarshal(b, &sec); err != nil {
		return err
	}
	*d = Duration(formatSeconds(sec))
	return nil
}

func formatSeconds(sec float64) string {
	total := int64(sec + 0.5)
	return fmt.Sprintf("%d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}

type UploadQueueItem struct {
	ID          int64  `json:"id"`
	FilePath    string `json:"filePath"`
	Status      string `json:"status"`
	RetryCount  int    `json:"retryCount"`
	LastAttempt string `json:"lastAttempt,omitempty"`
	Error       string `json:"error,omitempty"`
}

type SearchRequest struct {
	Type         string
	WithArchived bool
}

type Album struct {
	ID                    string `json:"id"`
	AlbumName             string `json:"albumName"`
	Description           string `json:"description"`
	AssetCount            int    `json:"assetCount"`
	AlbumThumbnailAssetID string `json:"albumThumbnailAssetId"`
}
