package llm

import "time"

// RequestMeasurement is one completed client attempt. Duration includes prefill
// and transport, excludes time between requests (tools and user interaction).
type RequestMeasurement struct {
	Server    string  `json:"server"`
	Requested string  `json:"requested"`
	Served    string  `json:"served,omitempty"`
	Seconds   float64 `json:"seconds"`
	Usage     Usage   `json:"usage"`
	Failed    bool    `json:"failed"`
}

func (c *Client) WithRequestObserver(observer func(RequestMeasurement)) *Client {
	c.requestObserver = observer
	return c
}
func (c *Client) observeRequest(start time.Time, served string, usage Usage, err error) {
	if c.requestObserver != nil {
		c.requestObserver(RequestMeasurement{Server: c.baseURL, Requested: c.model, Served: served, Seconds: time.Since(start).Seconds(), Usage: usage, Failed: err != nil})
	}
}
