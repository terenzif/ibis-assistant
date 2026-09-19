package ai

import (
	"context"
	"fmt"
	"strings"
)

// CloudPool tries cloud providers in order with failover.
type CloudPool struct {
	primary   string
	providers map[string]ReasoningProvider
	order     []string
}

// NewCloudPool builds a pool from named providers. order is fallback preference.
func NewCloudPool(primary string, order []string, providers map[string]ReasoningProvider) *CloudPool {
	clean := map[string]ReasoningProvider{}
	for k, v := range providers {
		if v != nil && v.IsFunctional() {
			clean[k] = v
		}
	}
	if primary == "" {
		for _, name := range order {
			if _, ok := clean[name]; ok {
				primary = name
				break
			}
		}
	}
	return &CloudPool{primary: primary, providers: clean, order: order}
}

func (p *CloudPool) Name() string { return "cloud_pool" }
func (p *CloudPool) IsFunctional() bool {
	return p != nil && len(p.providers) > 0
}
func (p *CloudPool) Stop() {
	for _, pr := range p.providers {
		pr.Stop()
	}
}

func (p *CloudPool) GenerateContent(ctx context.Context, contents []Content, cfg GenerationConfig) (Candidate, error) {
	if !p.IsFunctional() {
		return Candidate{}, fmt.Errorf("no cloud providers configured")
	}
	tryOrder := make([]string, 0, len(p.order)+1)
	if p.primary != "" {
		tryOrder = append(tryOrder, p.primary)
	}
	for _, name := range p.order {
		if name == p.primary {
			continue
		}
		tryOrder = append(tryOrder, name)
	}
	var lastErr error
	for _, name := range tryOrder {
		pr, ok := p.providers[name]
		if !ok {
			continue
		}
		cand, err := pr.GenerateContent(ctx, contents, cfg)
		if err == nil {
			return cand, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("cloud pool exhausted")
	}
	return Candidate{}, lastErr
}

// HasAny reports whether any named cloud is functional.
func (p *CloudPool) HasAny() bool { return p.IsFunctional() }

func normalizeCloudName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "openai" {
		return "openai_compat"
	}
	if s == "anthropic" {
		return "claude"
	}
	return s
}
