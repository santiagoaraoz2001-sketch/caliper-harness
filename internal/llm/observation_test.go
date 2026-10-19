package llm

import (
	"context"
	"testing"
)

func TestRequestObserverOnceAcrossPlainStreamToolsAndClone(t *testing.T) {
	for _, mode := range []string{"plain", "stream", "tools", "clone"} {
		t.Run(mode, func(t *testing.T) {
			body := `{"model":"actual-served","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":4}}`
			if mode != "plain" {
				body = "data: {\"model\":\"actual-served\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n"
			}
			c, _ := stubClient(t, 200, body)
			var measurements []RequestMeasurement
			c.WithRequestObserver(func(v RequestMeasurement) { measurements = append(measurements, v) })
			var err error
			switch mode {
			case "plain":
				_, _, _, err = c.Chat(context.Background(), nil)
			case "stream":
				_, _, _, err = c.ChatStream(context.Background(), nil, nil)
			case "tools":
				_, err = c.ChatTools(context.Background(), nil, nil, nil)
			case "clone":
				_, err = c.WithoutThinking().ChatTools(context.Background(), nil, nil, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(measurements) != 1 {
				t.Fatalf("%d observations", len(measurements))
			}
			v := measurements[0]
			// An in-memory transport can finish within one platform clock tick.
			if v.Failed || v.Served != "actual-served" || v.Requested != "fake-1" || v.Seconds < 0 || !v.Usage.Reported || v.Usage.Out != 4 {
				t.Fatalf("bad measurement: %+v", v)
			}
		})
	}
}
func TestRequestObserverRetainsFailureAndUnknownUsage(t *testing.T) {
	for _, status := range []int{200, 503} {
		c, _ := stubClient(t, status, `{"model":"served","choices":[{"message":{"content":"ok"}}]}`)
		var got []RequestMeasurement
		c.WithRequestObserver(func(v RequestMeasurement) { got = append(got, v) })
		_, _, _, err := c.Chat(context.Background(), nil)
		if len(got) != 1 || got[0].Usage.Reported || got[0].Failed != (err != nil) {
			t.Fatalf("status%d: %+v %v", status, got, err)
		}
	}
}
