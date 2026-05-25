package graph

type Presence struct {
	Availability  string `json:"availability"`
	Activity      string `json:"activity"`
	StatusMessage *StatusMessage `json:"statusMessage"`
}

type StatusMessage struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

func (c *Client) GetMyPresence() (*Presence, error) {
	var p Presence
	if err := c.Get("/me/presence", &p); err != nil {
		return nil, err
	}
	return &p, nil
}

