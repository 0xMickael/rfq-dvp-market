package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Request struct {
	Side   string `json:"side"`
	Unit   string `json:"unit"`
	Amount string `json:"amount"`
}

// Client transports Rust amounts as strings without numeric conversion.
type Client struct {
	URL  string
	HTTP *http.Client
}

func NewClient(url string) *Client {
	return &Client{URL: url, HTTP: &http.Client{Timeout: 2 * time.Second}}
}

func (c *Client) Quote(ctx context.Context, input Request) (int, []byte, error) {
	return c.Call(ctx, http.MethodPost, "/quotes", input)
}

func (c *Client) Call(ctx context.Context, method, path string, input any) (int, []byte, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return 0, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, c.URL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.HTTP.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	const maxResponse = 64 * 1024
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return 0, nil, err
	}
	if len(data) > maxResponse || !json.Valid(data) {
		return 0, nil, fmt.Errorf("invalid engine response")
	}
	switch response.StatusCode {
	case 200, 201, 400, 403, 404, 409, 410, 422:
		return response.StatusCode, data, nil
	default:
		return 0, nil, fmt.Errorf("statut moteur inattendu : %d", response.StatusCode)
	}
}
