package ai

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubReasoning struct {
	name string
	ok   bool
	err  error
	text string
}

func (s *stubReasoning) Name() string       { return s.name }
func (s *stubReasoning) IsFunctional() bool { return s.ok }
func (s *stubReasoning) Stop()              {}
func (s *stubReasoning) GenerateContent(ctx context.Context, contents []Content, cfg GenerationConfig) (Candidate, error) {
	if s.err != nil {
		return Candidate{}, s.err
	}
	return Candidate{Content: Content{Parts: []Part{{Text: s.text}}}}, nil
}

func TestRouterPrefersCloudOnCPUQuality(t *testing.T) {
	local := &stubReasoning{name: "ollama", ok: true, text: "local"}
	cloud := &stubReasoning{name: "gemini", ok: true, text: "cloud"}
	r := NewReasoningRouter(local, cloud, RouterConfig{UseCloudWhenNoGPU: true, CPUOnly: true})
	cand, err := r.GenerateContent(context.Background(), nil, GenerationConfig{RouteHint: "quality"})
	if err != nil {
		t.Fatal(err)
	}
	if cand.Content.Parts[0].Text != "cloud" {
		t.Fatalf("got %s", cand.Content.Parts[0].Text)
	}
}

func TestRouterContextOnly(t *testing.T) {
	r := NewReasoningRouter(nil, nil, RouterConfig{ContextOnly: true})
	_, err := r.GenerateContent(context.Background(), nil, GenerationConfig{})
	if !errors.Is(err, ErrContextOnly) {
		t.Fatalf("err=%v", err)
	}
}

func TestCloudPoolFailover(t *testing.T) {
	bad := &stubReasoning{name: "gemini", ok: true, err: errors.New("quota")}
	good := &stubReasoning{name: "claude", ok: true, text: "ok"}
	pool := NewCloudPool("gemini", []string{"gemini", "claude"}, map[string]ReasoningProvider{
		"gemini": bad, "claude": good,
	})
	cand, err := pool.GenerateContent(context.Background(), nil, GenerationConfig{})
	if err != nil || cand.Content.Parts[0].Text != "ok" {
		t.Fatalf("%v %v", cand, err)
	}
	_ = time.Second
}
