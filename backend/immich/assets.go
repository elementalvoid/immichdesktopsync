package immich

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"time"

	"immich-desktop-sync/backend/models"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	AccessToken string `json:"accessToken"`
	UserID      string `json:"userId"`
	UserEmail   string `json:"userEmail"`
	Name        string `json:"name"`
}

func (c *Client) Login(email, password string) (string, *models.User, error) {
	body, _ := json.Marshal(loginRequest{Email: email, Password: password})
	resp, err := c.do("POST", "/api/auth/login", bytes.NewReader(body), "application/json")
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	var lr loginResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return "", nil, err
	}
	user := &models.User{
		ID:    lr.UserID,
		Email: lr.UserEmail,
		Name:  lr.Name,
	}
	return lr.AccessToken, user, nil
}

func (c *Client) GetMe() (*models.User, error) {
	resp, err := c.do("GET", "/api/users/me", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var user models.User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (c *Client) GetServerVersion() (string, error) {
	resp, err := c.do("GET", "/api/server-info/version", nil, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var v struct {
		Major int `json:"major"`
		Minor int `json:"minor"`
		Patch int `json:"patch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch), nil
}

type searchMetadataPayload struct {
	Page         int    `json:"page"`
	Size         int    `json:"size"`
	WithArchived bool   `json:"withArchived,omitempty"`
	Type         string `json:"type,omitempty"`
}

type searchMetadataResponse struct {
	Assets struct {
		Total   int            `json:"total"`
		Count   int            `json:"count"`
		Page    int            `json:"page"`
		Pages   int            `json:"pages"`
		HasNext bool           `json:"hasNext"`
		Items   []models.Asset `json:"items"`
	} `json:"assets"`
}

func (c *Client) SearchMetadata(req models.SearchRequest) ([]models.Asset, error) {
	var all []models.Asset
	page := 1

	for {
		payload := searchMetadataPayload{
			Page:         page,
			Size:         1000,
			WithArchived: req.WithArchived,
			Type:         req.Type,
		}

		body, _ := json.Marshal(payload)
		resp, err := c.do("POST", "/api/search/metadata", bytes.NewReader(body), "application/json")
		if err != nil {
			return nil, err
		}

		var result searchMetadataResponse
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}

		all = append(all, result.Assets.Items...)
		if !result.Assets.HasNext {
			break
		}
		page++
	}

	return all, nil
}

// UploadFile streams the file to /api/assets. The multipart body is written
// through an io.Pipe, so the file is never held in memory. Field order (file
// part first) matches the Immich client.
func (c *Client) UploadFile(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}

	name := filepath.Base(filePath)
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	contentType := mw.FormDataContentType()

	writeBody := func() error {
		fw, err := mw.CreateFormFile("assetData", name)
		if err != nil {
			return err
		}
		if _, err := io.Copy(fw, f); err != nil {
			return err
		}
		fields := []struct{ name, value string }{
			{"deviceAssetId", fmt.Sprintf("%s-%d", name, info.Size())},
			{"deviceId", "immich-desktop-sync"},
			{"fileCreatedAt", info.ModTime().UTC().Format(time.RFC3339)},
			{"fileModifiedAt", info.ModTime().UTC().Format(time.RFC3339)},
			{"isFavorite", "false"},
		}
		for _, field := range fields {
			if err := writeField(mw, field.name, field.value); err != nil {
				return err
			}
		}
		return mw.Close()
	}

	go func() {
		if err := writeBody(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()

	resp, err := c.do("POST", "/api/assets", pr, contentType)
	if err != nil {
		_ = pr.CloseWithError(err) // unblock the writer goroutine
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.ID, nil
}

func (c *Client) GetAssetInfo(assetID string) (*models.Asset, error) {
	resp, err := c.do("GET", fmt.Sprintf("/api/assets/%s", assetID), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var asset models.Asset
	if err := json.NewDecoder(resp.Body).Decode(&asset); err != nil {
		return nil, err
	}
	return &asset, nil
}

func (c *Client) FetchThumbnail(assetID, size string) ([]byte, error) {
	if size == "" {
		size = "thumbnail"
	}
	resp, err := c.doThumb("GET", fmt.Sprintf("/api/assets/%s/thumbnail?size=%s", assetID, size))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// GetOriginal returns the original asset body. The caller owns the reader and
// must close it. The body is streamed, so the whole asset is never in memory.
func (c *Client) GetOriginal(assetID string) (io.ReadCloser, error) {
	resp, err := c.do("GET", fmt.Sprintf("/api/assets/%s/original", assetID), nil, "")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func writeField(mw *multipart.Writer, field, value string) error {
	fw, err := mw.CreateFormField(field)
	if err != nil {
		return err
	}
	_, err = fw.Write([]byte(value))
	return err
}
