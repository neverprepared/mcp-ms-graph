package graph

import (
	"fmt"
	"net/url"
	"strings"
)

type Person struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
	JobTitle          string `json:"jobTitle"`
	Department        string `json:"department"`
}

// escapeOData escapes single quotes for OData filter expressions.
func escapeOData(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func (c *Client) SearchPeople(query string) ([]Person, error) {
	safe := escapeOData(query)
	path := fmt.Sprintf("/users?$filter=startswith(displayName,'%s') or startswith(mail,'%s')&$top=15&$select=id,displayName,mail,jobTitle,department,userPrincipalName",
		url.QueryEscape(safe), url.QueryEscape(safe))
	var result GraphList[Person]
	if err := c.Get(path, &result); err != nil {
		return nil, err
	}
	return result.Value, nil
}

func (c *Client) CreateOneOnOneChat(userID string) (*Chat, error) {
	// First get our own user ID
	var me struct {
		ID string `json:"id"`
	}
	if err := c.Get("/me", &me); err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}

	body := map[string]interface{}{
		"chatType": "oneOnOne",
		"members": []map[string]interface{}{
			{
				"@odata.type":     "#microsoft.graph.aadUserConversationMember",
				"roles":           []string{"owner"},
				"user@odata.bind": fmt.Sprintf("https://graph.microsoft.com/v1.0/users('%s')", userID),
			},
			{
				"@odata.type":     "#microsoft.graph.aadUserConversationMember",
				"roles":           []string{"owner"},
				"user@odata.bind": fmt.Sprintf("https://graph.microsoft.com/v1.0/users('%s')", me.ID),
			},
		},
	}

	var chat Chat
	if err := c.Post("/chats", body, &chat); err != nil {
		return nil, err
	}
	return &chat, nil
}
