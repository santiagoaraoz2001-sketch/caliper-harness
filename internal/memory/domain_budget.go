package memory

import (
	"context"
	"errors"
	"fmt"
	"specific-harness/internal/llm"
	"strings"
)

// Builtin articles start around 1,000 tokens; enriched domain hops may use the
// user's 2,000-token ceiling, including preferences and navigation metadata.
const DomainTokens = 2000

const DomainNativeTokens = "native-token-ids"
const DomainUTF8Bytes = "utf8-bytes-conservative"

func ProjectGuide(key string) bool {
	if !strings.HasPrefix(key, "project/guides/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(key, "project/guides/"), "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	return true
}

type TokenCounter func(context.Context, string) (int, error)

// AdmitDomain applies only to a domain-guidance response, including its locator
// and navigation. Ordinary project facts and topical recall have other owners.
func AdmitDomain(ctx context.Context, count TokenCounter, payload []byte) error {
	_, err := AdmitDomainEnvelope(ctx, count, func(string) ([]byte, error) { return payload, nil })
	return err
}

// AdmitDomainEnvelope counts the same serialized domain values that the caller
// emits, including its basis label. Only a declared adapter capability gap uses
// the existing uncalibrated UTF-8-byte estimate; it is not an exact token claim.
func AdmitDomainEnvelope(ctx context.Context, count TokenCounter, encode func(string) ([]byte, error)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if count == nil {
		return "", fmt.Errorf("domain token count unavailable; selected coding model needs a supported tokenizer or explicit capability marker")
	}
	basis := DomainNativeTokens
	payload, err := encode(basis)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	n, err := count(ctx, string(payload))
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if errors.Is(err, llm.ErrTokenCountingUnsupported) {
		basis = DomainUTF8Bytes
		payload, err = encode(basis)
		n = len(payload)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("domain token count unavailable: %w", err)
	}
	if n < 0 {
		return "", fmt.Errorf("domain tokenizer returned an invalid count")
	}
	if n > DomainTokens {
		unit := "tokens"
		if basis == DomainUTF8Bytes {
			unit = "UTF-8 bytes (conservative estimate, not measured tokens)"
		}
		return basis, fmt.Errorf("domain guidance is %d %s; maximum is %d per retrieval; select one narrower section and follow its references", n, unit, DomainTokens)
	}
	return basis, nil
}
