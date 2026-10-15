package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type cancellationBody struct {
	io.Reader
	cause error
}

func (b cancellationBody) Read(p []byte) (int, error) {
	n, e := b.Reader.Read(p)
	if e == io.EOF {
		return n, b.cause
	}
	return n, e
}
func (b cancellationBody) Close() error { return nil }

func TestCancellationPresentationPreservesTransportCauseAndPartial(t *testing.T) {
	for _, runtime := range []string{RuntimeOpenAI, RuntimeMLX, RuntimeOllama} {
		for _, stream := range []bool{false, true} {
			for _, cause := range []error{context.Canceled, context.DeadlineExceeded, io.ErrUnexpectedEOF} {
				t.Run(fmt.Sprintf("%s/stream=%t/%v", runtime, stream, cause), func(t *testing.T) {
					body := `{"model":"m","choices":[{"message":{"content":"partial"}}]}`
					if stream {
						body = partialEvent
					}
					if runtime == RuntimeOllama {
						body = "{\"model\":\"m\",\"message\":{\"content\":\"partial\"}}\n"
					}
					c := New("http://fixture.invalid/v1", "m").WithRuntime(runtime)
					c.http = &http.Client{Transport: bodyErrorTransport{cancellationBody{strings.NewReader(body), cause}}}
					var reply, served string
					var usage Usage
					var err error
					if stream {
						reply, served, usage, err = c.ChatStream(context.Background(), nil, nil)
					} else {
						reply, served, usage, err = c.Chat(context.Background(), nil)
					}
					if !errors.Is(err, cause) || reply != "partial" || served != "m" || usage.Reported {
						t.Fatalf("partial/cause changed: %q %q %+v %v", reply, served, usage, err)
					}
					if cause == context.Canceled {
						if !strings.Contains(err.Error(), "cancelled") || strings.Contains(err.Error(), "retry") || strings.Contains(err.Error(), "broke") {
							t.Errorf("cancel presented as failure: %v", err)
						}
					} else if strings.Contains(err.Error(), "cancelled") {
						t.Errorf("noncancel error mislabeled: %v", err)
					}
				})
			}
		}
	}
}
