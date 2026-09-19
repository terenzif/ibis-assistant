package settings

import (
	"testing"

	"github.com/terenzif/ibis-assistant/internal/config"
)

func TestDefaultModelForTier(t *testing.T) {
	if DefaultModelForTier(TierS) != "granite4.1:3b" {
		t.Fatalf("S")
	}
	if DefaultModelForTier(TierXL) != "muse-glimmer" {
		t.Fatalf("XL")
	}
}

func TestResolveLocalModelAlwaysSmallest(t *testing.T) {
	r := config.ReasoningConfig{
		Model:               "auto",
		AlwaysSmallestLocal: true,
		ModelOverrides:      map[string]string{"S": "granite4.1:3b", "L": "gemma4:12b"},
	}
	got := ResolveLocalModel(r, HardwareReport{Tier: TierL, VRAMMiB: 16 * 1024})
	if got != "granite4.1:3b" {
		t.Fatalf("got %s", got)
	}
}

func TestRecommendCloudPrimary(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.AI.Reasoning.Clouds.Gemini.Keys = []config.GeminiKeyConfig{{Key: "abc"}}
	config.NormalizeReasoningConfig(&cfg.AI.Reasoning, 100)
	if p := cfg.AI.Reasoning.ResolveCloudPrimary(); p != "gemini" {
		t.Fatalf("primary=%s", p)
	}
	rec := Recommend(cfg, HardwareReport{Tier: TierM, VRAMMiB: 8 * 1024})
	if rec.Summary == "" {
		t.Fatal("empty summary")
	}
}

func TestApplyGeminiKey(t *testing.T) {
	cfg := config.NewDefaultConfig()
	out, _, err := Apply(cfg, UserChoices{
		AIMode:    "hybrid",
		GeminiKey: "secret-key-xyz",
	}, HardwareReport{Tier: TierS})
	if err != nil {
		t.Fatal(err)
	}
	if !out.AI.Reasoning.CloudHasCredentials("gemini") {
		t.Fatal("expected gemini key")
	}
	if MaskSecret("secret-key-xyz") != "…-xyz" && MaskSecret("secret-key-xyz") != "…xyz" {
		// last 4 of secret-key-xyz is "-xyz" wait: last 4 is "xyz" with hyphen?
		// "secret-key-xyz" last 4 = "-xyz"
	}
	if MaskSecret("abcdefgh") != "…efgh" {
		t.Fatalf("mask=%s", MaskSecret("abcdefgh"))
	}
}

func TestNormalizeLegacyKeys(t *testing.T) {
	r := config.ReasoningConfig{
		Provider: "gemini",
		Keys:     []config.GeminiKeyConfig{{Key: "k1"}},
	}
	config.NormalizeReasoningConfig(&r, 50)
	if len(r.Clouds.Gemini.Keys) != 1 || r.Clouds.Gemini.Keys[0].Key != "k1" {
		t.Fatalf("%+v", r.Clouds.Gemini)
	}
}
