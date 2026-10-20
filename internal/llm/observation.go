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

// WithStreamObserver reports observed stream activity, never inferred progress
// or generated text. An empty stage clears the indicator when the attempt ends.
func (c *Client) WithStreamObserver(observer func(string)) *Client {
	c.streamObserver = observer
	return c
}

func (c *Client) streamActivity() func(string) {
	last := ""
	observe := func(stage string) {
		// Keepalives prove the connection is active, not a new generation phase.
		if stage == last || stage == "active" && last != "waiting" {
			return
		}
		last = stage
		if c.streamObserver != nil {
			c.streamObserver(stage)
		}
	}
	observe("waiting")
	return observe
}
func (c *Client) observeRequest(start time.Time, served string, usage Usage, err error) {
	if c.requestObserver != nil {
		c.requestObserver(RequestMeasurement{Server: c.baseURL, Requested: c.model, Served: served, Seconds: time.Since(start).Seconds(), Usage: usage, Failed: err != nil})
	}
}
