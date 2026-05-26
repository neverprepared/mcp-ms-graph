package graph

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const batchSize = 20
const msgSelectFields = "$select=id,subject,from,toRecipients,receivedDateTime,body,isRead,hasAttachments"

// BatchGetMessages fetches multiple messages in parallel using Graph's $batch
// endpoint (max 20 per request). Returns a map of message ID → message.
// Messages that 404 or error are omitted from the map.
func (c *Client) BatchGetMessages(ids []string) (map[string]*MailMessage, error) {
	results := make(map[string]*MailMessage, len(ids))

	for i := 0; i < len(ids); i += batchSize {
		end := i + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[i:end]

		requests := make([]BatchRequest, len(chunk))
		for j, id := range chunk {
			requests[j] = BatchRequest{
				ID:     strconv.Itoa(j),
				Method: "GET",
				URL:    fmt.Sprintf("/me/messages/%s?%s", id, msgSelectFields),
			}
		}

		responses, err := c.Batch(requests)
		if err != nil {
			return nil, err
		}

		for _, resp := range responses {
			if resp.Status != 200 {
				continue
			}
			idx, err := strconv.Atoi(resp.ID)
			if err != nil || idx < 0 || idx >= len(chunk) {
				continue
			}
			var msg MailMessage
			if err := json.Unmarshal(resp.Body, &msg); err == nil {
				results[chunk[idx]] = &msg
			}
		}
	}
	return results, nil
}

type MailMessage struct {
	ID               string      `json:"id"`
	Subject          string      `json:"subject"`
	From             *MailFrom   `json:"from"`
	ToRecipients     []Recipient `json:"toRecipients"`
	ReceivedDateTime time.Time   `json:"receivedDateTime"`
	Body             MailBody    `json:"body"`
	BodyPreview      string      `json:"bodyPreview"`
	IsRead           bool        `json:"isRead"`
	HasAttachments   bool        `json:"hasAttachments"`
	Importance       string      `json:"importance"`
}

type MailFrom struct {
	EmailAddress EmailAddr `json:"emailAddress"`
}

type Recipient struct {
	EmailAddress EmailAddr `json:"emailAddress"`
}

type MailBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

func (c *Client) ListInboxMessages(top int, since string, unreadOnly bool) ([]MailMessage, error) {
	sel := "$select=id,subject,from,toRecipients,receivedDateTime,bodyPreview,isRead,hasAttachments,importance"

	var sinceTime time.Time
	if since != "" {
		sinceTime, _ = time.Parse(time.RFC3339, since)
	}

	// Build server-side filter
	var filters []string
	if unreadOnly {
		filters = append(filters, "isRead+eq+false")
	}
	if since != "" {
		filters = append(filters, fmt.Sprintf("receivedDateTime+ge+%s", since))
	}

	if len(filters) > 0 {
		filterStr := strings.Join(filters, "+and+")
		path := fmt.Sprintf("/me/mailFolders/inbox/messages?$top=%d&$filter=%s&$orderby=receivedDateTime+desc&%s", top, filterStr, sel)
		var result GraphList[MailMessage]
		if err := c.Get(path, &result); err == nil {
			return result.Value, nil
		}
	}

	// Fallback: fetch all, filter client-side
	fetchCount := 100
	path := fmt.Sprintf("/me/mailFolders/inbox/messages?$top=%d&$orderby=receivedDateTime+desc&%s", fetchCount, sel)
	var result GraphList[MailMessage]
	if err := c.Get(path, &result); err != nil {
		return nil, err
	}
	var filtered []MailMessage
	for _, m := range result.Value {
		if unreadOnly && m.IsRead {
			continue
		}
		if !sinceTime.IsZero() && m.ReceivedDateTime.Before(sinceTime) {
			continue
		}
		filtered = append(filtered, m)
	}
	return filtered, nil
}

func (c *Client) GetMailMessage(id string) (*MailMessage, error) {
	path := fmt.Sprintf("/me/messages/%s", id)
	var msg MailMessage
	if err := c.Get(path, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (c *Client) SendMail(to, subject, body string) error {
	payload := map[string]interface{}{
		"message": map[string]interface{}{
			"subject": subject,
			"body": map[string]string{
				"contentType": "text",
				"content":     body,
			},
			"toRecipients": []map[string]interface{}{
				{
					"emailAddress": map[string]string{
						"address": to,
					},
				},
			},
		},
	}
	return c.Post("/me/sendMail", payload, nil)
}

func (c *Client) DeleteMessage(id string) error {
	url := BaseURL + fmt.Sprintf("/me/messages/%s", id)

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return &GraphError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return nil
}

func (c *Client) MarkMessageRead(id string, isRead bool) error {
	return c.Patch(fmt.Sprintf("/me/messages/%s", id), map[string]interface{}{
		"isRead": isRead,
	})
}

func (c *Client) ReplyToMessage(id, comment string) error {
	payload := map[string]interface{}{
		"comment": comment,
	}
	return c.Post(fmt.Sprintf("/me/messages/%s/reply", id), payload, nil)
}

type MailFolder struct {
	ID               string `json:"id"`
	DisplayName      string `json:"displayName"`
	ParentFolderID   string `json:"parentFolderId"`
	ChildFolderCount int    `json:"childFolderCount"`
	TotalItemCount   int    `json:"totalItemCount"`
	UnreadItemCount  int    `json:"unreadItemCount"`
}

type InboxStats struct {
	Unread int `json:"unread"`
	Total  int `json:"total"`
}

// GetInboxStats returns unread and total message counts from the inbox folder
// object — much faster than counting messages.
func (c *Client) GetInboxStats() (*InboxStats, error) {
	var folder struct {
		TotalItemCount  int `json:"totalItemCount"`
		UnreadItemCount int `json:"unreadItemCount"`
	}
	if err := c.Get("/me/mailFolders/inbox?$select=totalItemCount,unreadItemCount", &folder); err != nil {
		return nil, err
	}
	return &InboxStats{Unread: folder.UnreadItemCount, Total: folder.TotalItemCount}, nil
}

// ListMailFolders returns the top-level mail folders, including well-known
// folders like Inbox, Archive, Deleted Items, Drafts, Sent Items, Junk Email.
func (c *Client) ListMailFolders() ([]MailFolder, error) {
	var result GraphList[MailFolder]
	// includeHiddenFolders surfaces folders like "Archive" that Outlook hides by default.
	if err := c.Get("/me/mailFolders?$top=100&includeHiddenFolders=true", &result); err != nil {
		return nil, err
	}
	return result.Value, nil
}

// MoveMessage moves an email to the given folder. destinationID accepts either
// a folder ID returned from ListMailFolders or a well-known folder name
// (inbox, archive, deleteditems, drafts, junkemail, sentitems, outbox).
// Returns the new message ID (Graph assigns a new ID after a move).
func (c *Client) MoveMessage(messageID, destinationID string) (string, error) {
	var moved MailMessage
	err := c.Post(fmt.Sprintf("/me/messages/%s/move", messageID),
		map[string]interface{}{"destinationId": destinationID},
		&moved)
	if err != nil {
		return "", err
	}
	return moved.ID, nil
}
