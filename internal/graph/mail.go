package graph

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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
