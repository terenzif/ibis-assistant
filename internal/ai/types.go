package ai

import (
	"context"
	"errors"
	"time"
)

// EmbeddingProvider defines the interface for generating semantic vector embeddings.
type EmbeddingProvider interface {
	Name() string
	IsFunctional() bool
	EmbedText(ctx context.Context, text string) ([]float32, error)
	BatchEmbedText(ctx context.Context, texts []string) ([][]float32, error)
	Stop()
}

// ReasoningProvider defines the interface for content generation and text completion.
type ReasoningProvider interface {
	Name() string
	IsFunctional() bool
	GenerateContent(ctx context.Context, contents []Content, config GenerationConfig) (Candidate, error)
	Stop()
}

// Common DTOs and structs used across providers and services

type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

type Part struct {
	Text string `json:"text"`
}

type GenerationConfig struct {
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	// RouteHint guides hybrid routing: bulk | quality | auto (empty = auto).
	RouteHint string `json:"routeHint,omitempty"`
}

// ErrContextOnly is returned when no LLM is available and callers should pack retrieval context.
var ErrContextOnly = errors.New("context only: no reasoning provider available")

type Candidate struct {
	Content       Content        `json:"content"`
	FinishReason  string         `json:"finishReason"`
	UsageMetadata *UsageMetadata `json:"-"`
}

type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type BatchJobStatus struct {
	Name       string
	Done       bool
	Error      error
	Embeddings [][]float32
	Candidates []Candidate
}

type KeyConfig struct {
	Key           string        `json:"key"`
	RPM           int           `json:"rpm"`
	TPM           int           `json:"tpm"`
	RPD           int           `json:"rpd"`
	Owner         string        `json:"owner"`
	FlushInterval time.Duration `json:"-"`
	AllowOverage  bool          `json:"-"`
}

// Internal job structs for worker pool coordination

type EmbedJob struct {
	Texts      []string
	ResultChan chan EmbedResult
}

type EmbedResult struct {
	Embeddings [][]float32
	Error      error
}

type GenerateJob struct {
	Contents   []Content
	Config     GenerationConfig
	ResultChan chan GenerateResult
}

type GenerateResult struct {
	Response Candidate
	Error    error
}
