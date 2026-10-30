package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"specific-harness/internal/llm"
	"strings"
)

// New sections start at 1,000 tokens. A later version of an existing section
// may include preferences and enrichment up to 2,000 tokens. The selected
// retrieval, including projected aliases and navigation, also stays within 2,000.
const DomainInitialTokens = 1000
const DomainTokens = 2000

const DomainNativeTokens = "native-token-ids"
const DomainUTF8Bytes = "utf8-bytes-conservative"

var ErrDomainBudget = errors.New("domain guidance budget exceeded")

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

// DomainSectionTokens uses immutable section lineage, not a prose classifier.
// A newly added or renamed anchor starts at the initial limit even in a revision.
func DomainSectionTokens(previousBody, sectionID string) int {
	if previousBody != "" && len(Sections(previousBody, "", sectionID)) > 0 {
		return DomainTokens
	}
	return DomainInitialTokens
}

// AdmitDomainSections applies the authoring contract to complete section values.
// This canonical envelope includes the locator and direct navigation; it never
// expands referenced bodies. The actual projected retrieval has its own ceiling.
func AdmitDomainSections(ctx context.Context, count TokenCounter, key, previousBody string, sections []Section, refs []Reference) (string, error) {
	basis := ""
	for _, section := range sections {
		var err error
		basis, err = AdmitDomainEnvelopeLimit(ctx, count, DomainSectionTokens(previousBody, section.ID), func(basis string) ([]byte, error) {
			return json.Marshal(struct {
				Key        string      `json:"key"`
				Section    string      `json:"section"`
				Title      string      `json:"title,omitempty"`
				Body       string      `json:"body"`
				References []Reference `json:"references,omitempty"`
				Basis      string      `json:"domain_budget_basis"`
			}{key, section.ID, section.Title, section.Body, SectionReferences(refs, section.ID), basis})
		})
		if err != nil {
			return basis, fmt.Errorf("domain section %s#%s: %w", key, section.ID, err)
		}
	}
	return basis, nil
}

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
	return AdmitDomainEnvelopeLimit(ctx, count, DomainTokens, encode)
}

// AdmitDomainEnvelopeLimit shares exact/fallback accounting with both section
// authoring and selected delivery. Callers choose the initial or enriched limit.
func AdmitDomainEnvelopeLimit(ctx context.Context, count TokenCounter, limit int, encode func(string) ([]byte, error)) (string, error) {
	if limit != DomainInitialTokens && limit != DomainTokens {
		return "", fmt.Errorf("invalid domain token limit %d", limit)
	}
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
	if n > limit {
		unit := "tokens"
		if basis == DomainUTF8Bytes {
			unit = "UTF-8 bytes (conservative estimate, not measured tokens)"
		}
		return basis, fmt.Errorf("%w: domain guidance is %d %s; maximum is %d; select one narrower section and follow its references", ErrDomainBudget, n, unit, limit)
	}
	return basis, nil
}
