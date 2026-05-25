package graph

import "time"

type GraphList[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"@odata.nextLink"`
}

type Chat struct {
	ID                   string        `json:"id"`
	Topic                string        `json:"topic"`
	ChatType             string        `json:"chatType"`
	LastUpdatedDateTime  time.Time     `json:"lastUpdatedDateTime"`
	Members              []ChatMember  `json:"members"`
	LastMessagePreview   *MessagePreview `json:"lastMessagePreview"`
}

type ChatMember struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

type MessagePreview struct {
	Body      PreviewBody `json:"body"`
	CreatedDateTime time.Time `json:"createdDateTime"`
}

type PreviewBody struct {
	Content string `json:"content"`
}

type ChatMessage struct {
	ID              string      `json:"id"`
	CreatedDateTime time.Time   `json:"createdDateTime"`
	Body            MessageBody `json:"body"`
	From            *From       `json:"from"`
}

type MessageBody struct {
	Content     string `json:"content"`
	ContentType string `json:"contentType"`
}

type From struct {
	User *User `json:"user"`
}

type User struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
}
