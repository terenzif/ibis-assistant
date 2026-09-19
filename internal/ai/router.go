package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RouterConfig configures hybrid local/cloud routing.
type RouterConfig struct {
	UseCloudWhenNoGPU bool
	CPUOnly           bool
	LocalTimeout      time.Duration
	ContextOnly       bool
}

// ReasoningRouter implements hybrid routing between local and cloud pools.
type ReasoningRouter struct {
	local  ReasoningProvider
	cloud  ReasoningProvider
	cfg    RouterConfig
}

// NewReasoningRouter creates a hybrid router. local and/or cloud may be nil.
func NewReasoningRouter(local, cloud ReasoningProvider, cfg RouterConfig) *ReasoningRouter {
	if cfg.LocalTimeout <= 0 {
		cfg.LocalTimeout = 120 * time.Second
	}
	return &ReasoningRouter{local: local, cloud: cloud, cfg: cfg}
}

func (r *ReasoningRouter) Name() string { return "hybrid" }
func (r *ReasoningRouter) IsFunctional() bool {
	return (r.local != nil && r.local.IsFunctional()) || (r.cloud != nil && r.cloud.IsFunctional())
}
func (r *ReasoningRouter) Stop() {
	if r.local != nil {
		r.local.Stop()
	}
	if r.cloud != nil {
		r.cloud.Stop()
	}
}

func (r *ReasoningRouter) GenerateContent(ctx context.Context, contents []Content, cfg GenerationConfig) (Candidate, error) {
	hint := strings.ToLower(strings.TrimSpace(cfg.RouteHint))
	if hint == "" {
		hint = "auto"
	}
	preferCloud := hint == "quality" || (hint == "auto" && r.cfg.UseCloudWhenNoGPU && r.cfg.CPUOnly)

	tryCloud := func() (Candidate, error) {
		if r.cloud == nil || !r.cloud.IsFunctional() {
			return Candidate{}, fmt.Errorf("cloud unavailable")
		}
		return r.cloud.GenerateContent(ctx, contents, cfg)
	}
	tryLocal := func() (Candidate, error) {
		if r.local == nil || !r.local.IsFunctional() {
			return Candidate{}, fmt.Errorf("local unavailable")
		}
		lctx := ctx
		var cancel context.CancelFunc
		if r.cfg.LocalTimeout > 0 {
			lctx, cancel = context.WithTimeout(ctx, r.cfg.LocalTimeout)
			defer cancel()
		}
		return r.local.GenerateContent(lctx, contents, cfg)
	}

	var first, second func() (Candidate, error)
	if preferCloud && r.cloud != nil && r.cloud.IsFunctional() {
		first, second = tryCloud, tryLocal
	} else if hint == "bulk" || hint == "bulk_local" {
		first, second = tryLocal, tryCloud
	} else if preferCloud {
		first, second = tryCloud, tryLocal
	} else {
		first, second = tryLocal, tryCloud
	}

	cand, err := first()
	if err == nil {
		return cand, nil
	}
	cand2, err2 := second()
	if err2 == nil {
		return cand2, nil
	}
	if r.cfg.ContextOnly {
		return Candidate{}, ErrContextOnly
	}
	return Candidate{}, errors.Join(err, err2)
}
