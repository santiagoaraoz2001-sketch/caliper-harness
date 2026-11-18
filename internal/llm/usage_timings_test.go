package llm

import (
	"context"
	"testing"
)

func TestUsagePreservesProviderCacheAndTimings(t *testing.T) {
	body := "data: {\"model\":\"fake-1\",\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n" +
		"data: {\"model\":\"fake-1\",\"choices\":[],\"usage\":{\"prompt_tokens\":417,\"completion_tokens\":97,\"prompt_tokens_details\":{\"cached_tokens\":365}},\"timings\":{\"prompt_ms\":96,\"predicted_ms\":1072}}\n\n" +
		"data: [DONE]\n\n"
	c, _ := stubClient(t, 200, body)
	_, _, usage, err := c.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if usage != (Usage{In: 417, Out: 97, Cached: 365, Reported: true, PromptMS: 96, PredictedMS: 1072}) {
		t.Fatalf("provider cache/timings lost: %+v", usage)
	}
}
