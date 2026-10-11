package models

type Config struct {
	ServerURL       string `json:"serverUrl"`
	AccessToken     string `json:"accessToken"`
	DownloadsFolder string `json:"downloadsFolder"`
}
