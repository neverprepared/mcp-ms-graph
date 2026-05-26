package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GetUnreadChatCount returns the number of chats that have messages newer than
// the user's last-read timestamp (via the viewpoint property).
func (c *Client) GetUnreadChatCount() (int, error) {
	path := "/me/chats?$select=id,viewpoint&$expand=lastMessagePreview&$top=50"
	var result GraphList[Chat]
	if err := c.Get(path, &result); err != nil {
		return 0, err
	}
	count := 0
	for _, chat := range result.Value {
		if chat.Viewpoint == nil || chat.LastMessagePreview == nil {
			continue
		}
		if chat.LastMessagePreview.CreatedDateTime.After(chat.Viewpoint.LastMessageReadDateTime) {
			count++
		}
	}
	return count, nil
}

func (c *Client) ListChats(limit int) ([]Chat, error) {
	path := fmt.Sprintf("/me/chats?$expand=members,lastMessagePreview&$top=%d", limit)
	var result GraphList[Chat]
	if err := c.Get(path, &result); err != nil {
		return nil, err
	}
	return result.Value, nil
}

func (c *Client) GetChatMessages(chatID string, limit int) ([]ChatMessage, error) {
	path := fmt.Sprintf("/me/chats/%s/messages?$top=%d", chatID, limit)
	var result GraphList[ChatMessage]
	if err := c.Get(path, &result); err != nil {
		return nil, err
	}
	return result.Value, nil
}

func (c *Client) SendChatMessage(chatID string, content string) (*ChatMessage, error) {
	url := BaseURL + fmt.Sprintf("/me/chats/%s/messages", chatID)

	body := map[string]interface{}{
		"body": map[string]string{
			"content": content,
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal message: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &GraphError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var msg ChatMessage
	if err := json.Unmarshal(respBody, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &msg, nil
}
