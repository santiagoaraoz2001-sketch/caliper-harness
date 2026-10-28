package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"specific-harness/internal/llm"
)

func TestDomainFallbackBoundsExactUnicodeEnvelopeWithBasis(t *testing.T) {
	for _, size := range []int{2000, 2001} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			body := "漢🚀"
			var emitted []byte
			encode := func(basis string) ([]byte, error) {
				var err error
				emitted, err = json.Marshal(map[string]any{"body": body, "links": []string{"knowledge:project/guides/ui/index#shape"}, "basis": basis})
				return emitted, err
			}
			initial, _ := encode(DomainUTF8Bytes)
			body += strings.Repeat("x", size-len(initial))
			calls := 0
			basis, err := AdmitDomainEnvelope(t.Context(), func(context.Context, string) (int, error) {
				calls++
				return 0, fmt.Errorf("adapter: %w", llm.ErrTokenCountingUnsupported)
			}, encode)
			if basis != DomainUTF8Bytes || calls != 1 || len(emitted) != size || (err != nil) != (size > DomainTokens) {
				t.Fatalf("basis=%q calls=%d bytes=%d err=%v", basis, calls, len(emitted), err)
			}
			if err != nil && !strings.Contains(err.Error(), "not measured tokens") {
				t.Fatal("fallback presented as measured tokens", err)
			}
		})
	}
}

func TestDomainFallbackNeverMasksMissingNativeFailureOrCancellation(t *testing.T) {
	encode := func(basis string) ([]byte, error) {
		return json.Marshal(map[string]string{"body": "small", "basis": basis})
	}
	for _, count := range []TokenCounter{nil, func(context.Context, string) (int, error) { return 0, errors.New("native token counting unsupported") }, func(context.Context, string) (int, error) { return -1, nil }} {
		if basis, err := AdmitDomainEnvelope(t.Context(), count, encode); err == nil || basis != "" {
			t.Fatalf("unmarked failure admitted as %q: %v", basis, err)
		}
	}
	for _, cancelAt := range []string{"before", "native-count", "fallback-serialization"} {
		t.Run(cancelAt, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelAt == "before" {
				cancel()
			}
			count := func(context.Context, string) (int, error) {
				if cancelAt == "before" {
					t.Fatal("cancelled counter called")
				}
				if cancelAt == "native-count" {
					cancel()
				}
				return 0, llm.ErrTokenCountingUnsupported
			}
			basis, err := AdmitDomainEnvelope(ctx, count, func(basis string) ([]byte, error) {
				if basis == DomainUTF8Bytes && cancelAt == "fallback-serialization" {
					cancel()
				}
				return encode(basis)
			})
			if !errors.Is(err, context.Canceled) || basis != "" {
				t.Fatalf("cancel lost: %q %v", basis, err)
			}
		})
	}
}

func TestDomainNativeCountKeepsExactUnitsAndOneCall(t *testing.T) {
	calls := 0
	basis, err := AdmitDomainEnvelope(t.Context(), func(_ context.Context, payload string) (int, error) {
		calls++
		if !strings.Contains(payload, DomainNativeTokens) || len(payload) <= DomainTokens {
			t.Fatal("wrong counted native envelope")
		}
		return DomainTokens, nil
	}, func(basis string) ([]byte, error) {
		return json.Marshal(map[string]string{"body": strings.Repeat("漢", 2001), "basis": basis})
	})
	if err != nil || basis != DomainNativeTokens || calls != 1 {
		t.Fatalf("native basis/count changed: %q %d %v", basis, calls, err)
	}
}
