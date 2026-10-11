package immich

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL     string
	token       string
	httpClient  *http.Client
	thumbClient *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		thumbClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *Client) SetToken(token string) { c.token = token }
func (c *Client) Token() string         { return c.token }

func (c *Client) do(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	return c.doWith(context.Background(), c.httpClient, method, path, body, contentType, -1)
}

func (c *Client) doThumb(method, path string) (*http.Response, error) {
	return c.doWith(context.Background(), c.thumbClient, method, path, nil, "", -1)
}

// doWith sends the request. ctx cancels it. contentLength < 0 leaves the length
// unknown, so a non-nil body is sent chunked. A value >= 0 sets
// Content-Length, which is safer behind proxies than chunked.
func (c *Client) doWith(ctx context.Context, hc *http.Client, method, path string, body io.Reader, contentType string, contentLength int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if contentLength >= 0 {
		req.ContentLength = contentLength
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("immich API error %d: %s", resp.StatusCode, string(b))
	}
	return resp, nil
}
