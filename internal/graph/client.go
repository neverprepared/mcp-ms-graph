package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
)

const BaseURL = "https://graph.microsoft.com/v1.0"

type Client struct {
	httpClient *http.Client
}

func NewClient(ts oauth2.TokenSource) *Client {
	return &Client{
		httpClient: oauth2.NewClient(nil, ts),
	}
}

type GraphError struct {
	StatusCode int
	Body       string
}

func (e *GraphError) Error() string {
	return fmt.Sprintf("graph api error (%d): %s", e.StatusCode, e.Body)
}

func (c *Client) Post(path string, body interface{}, result interface{}) error {
	url := BaseURL + path

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	resp, err := c.httpClient.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("graph request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &GraphError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}

	return nil
}

func (c *Client) Patch(path string, body interface{}) error {
	url := BaseURL + path

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequest("PATCH", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("graph request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &GraphError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	return nil
}

func (c *Client) Get(path string, result interface{}) error {
	url := BaseURL + path

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("graph request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &GraphError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}

	return nil
}
