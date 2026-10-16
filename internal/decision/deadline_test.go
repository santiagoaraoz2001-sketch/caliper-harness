package decision

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestJudgeTransmitsEffectiveDeadlineAndInputLimit(t *testing.T) {
	for _, parentEarlier := range []bool{false, true} {
		st := &stubTransport{status: 200, body: `{"pick":0,"option":"a","confidence":0.9,"model":"m","device":"cpu"}`}
		c := stubClient(st).WithInputLimit(15999)
		c.http.Timeout = time.Minute
		parent := time.Now().Add(2 * time.Minute)
		if parentEarlier {
			parent = time.Now().Add(10 * time.Second)
		}
		ctx, cancel := context.WithDeadline(context.Background(), parent)
		before := time.Now()
		_, err := c.Judge(ctx, Request{Context: "ctx", Options: []string{"a", "b"}})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		var wire judgeRequest
		if err = json.Unmarshal(st.seenBody, &wire); err != nil {
			t.Fatal(err)
		}
		deadline := time.UnixMilli(wire.DeadlineUnixMS)
		if wire.MaxInputTokens != 15999 {
			t.Fatalf("limit=%d", wire.MaxInputTokens)
		}
		if parentEarlier {
			if wire.DeadlineUnixMS != parent.UnixMilli() {
				t.Fatalf("deadline=%v parent=%v", deadline, parent)
			}
		} else if deadline.Before(before.Add(time.Minute-time.Millisecond)) || deadline.After(time.Now().Add(time.Minute)) {
			t.Fatalf("deadline not bounded by client timeout: %v", deadline)
		}
	}
}
