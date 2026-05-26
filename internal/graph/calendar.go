package graph

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

type Event struct {
	ID               string       `json:"id"`
	Subject          string       `json:"subject"`
	Start            DateTimeZone `json:"start"`
	End              DateTimeZone `json:"end"`
	Location         *Location    `json:"location"`
	IsOnlineMeeting  bool         `json:"isOnlineMeeting"`
	OnlineMeetingURL string       `json:"onlineMeetingUrl"`
	OnlineMeeting    *OnlineMtg   `json:"onlineMeeting"`
	Organizer        *Organizer   `json:"organizer"`
	IsAllDay              bool            `json:"isAllDay"`
	IsCancelled           bool            `json:"isCancelled"`
	ShowAs                string          `json:"showAs"`
	Sensitivity           string          `json:"sensitivity"`
	Importance            string          `json:"importance"`
	BodyPreview           string          `json:"bodyPreview"`
	OnlineMeetingProvider string          `json:"onlineMeetingProvider"`
	SeriesMasterID        string          `json:"seriesMasterId"`
	Recurrence            *struct{}       `json:"recurrence"`
	ResponseStatus        *ResponseStatus `json:"responseStatus"`
	Attendees             []Attendee      `json:"attendees"`
}

type DateTimeZone struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

type Location struct {
	DisplayName string `json:"displayName"`
}

type OnlineMtg struct {
	JoinURL string `json:"joinUrl"`
}

type Organizer struct {
	EmailAddress EmailAddr `json:"emailAddress"`
}

type EmailAddr struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type ResponseStatus struct {
	Response string `json:"response"`
}

type Attendee struct {
	EmailAddress   EmailAddr       `json:"emailAddress"`
	Type           string          `json:"type"`
	Status         *ResponseStatus `json:"status"`
}

func (e Event) StartTime() time.Time {
	t, _ := time.Parse("2006-01-02T15:04:05.0000000", e.Start.DateTime)
	return t
}

func (e Event) EndTime() time.Time {
	t, _ := time.Parse("2006-01-02T15:04:05.0000000", e.End.DateTime)
	return t
}

func (e Event) JoinURL() string {
	if e.OnlineMeeting != nil && e.OnlineMeeting.JoinURL != "" {
		return e.OnlineMeeting.JoinURL
	}
	return e.OnlineMeetingURL
}

func (c *Client) AcceptEvent(eventID string) error {
	return c.Post(fmt.Sprintf("/me/events/%s/accept", eventID),
		map[string]interface{}{"sendResponse": true}, nil)
}

func (c *Client) DeclineEvent(eventID string) error {
	return c.Post(fmt.Sprintf("/me/events/%s/decline", eventID),
		map[string]interface{}{"sendResponse": true}, nil)
}

func (c *Client) TentativelyAcceptEvent(eventID string) error {
	return c.Post(fmt.Sprintf("/me/events/%s/tentativelyAccept", eventID),
		map[string]interface{}{"sendResponse": true}, nil)
}

func (c *Client) CreateEvent(subject string, start, end time.Time, attendees []string, isOnline bool, location string, body string) (*Event, error) {
	atts := make([]map[string]interface{}, len(attendees))
	for i, email := range attendees {
		atts[i] = map[string]interface{}{
			"emailAddress": map[string]string{"address": email},
			"type":         "required",
		}
	}

	payload := map[string]interface{}{
		"subject": subject,
		"start": map[string]string{
			"dateTime": start.Format("2006-01-02T15:04:05"),
			"timeZone": "UTC",
		},
		"end": map[string]string{
			"dateTime": end.Format("2006-01-02T15:04:05"),
			"timeZone": "UTC",
		},
		"attendees":       atts,
		"isOnlineMeeting": isOnline,
	}
	if location != "" {
		payload["location"] = map[string]string{"displayName": location}
	}
	if body != "" {
		payload["body"] = map[string]string{"contentType": "text", "content": body}
	}

	var event Event
	if err := c.Post("/me/events", payload, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

func (c *Client) DeleteEvent(eventID string) error {
	url := BaseURL + fmt.Sprintf("/me/events/%s", eventID)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return &GraphError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return nil
}

func (c *Client) GetCalendarView(start, end time.Time) ([]Event, error) {
	path := fmt.Sprintf("/me/calendarView?startDateTime=%s&endDateTime=%s&$orderby=start/dateTime&$top=25&$select=id,subject,start,end,location,isOnlineMeeting,onlineMeetingUrl,onlineMeeting,organizer,isAllDay,isCancelled,showAs,sensitivity,importance,bodyPreview,onlineMeetingProvider,seriesMasterId,recurrence,responseStatus,attendees",
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339))
	var result GraphList[Event]
	if err := c.Get(path, &result); err != nil {
		return nil, err
	}
	return result.Value, nil
}
