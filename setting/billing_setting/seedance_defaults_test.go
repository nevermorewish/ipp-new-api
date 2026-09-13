package billing_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedanceDefaultBillingUsesTokenResolutionAndVideoInput(t *testing.T) {
	tests := []struct {
		model      string
		resolution string
		videoInput string
		wantPrice  float64
		wantTier   string
	}{
		{model: "doubao-seedance-2.0", resolution: "720p", videoInput: "none", wantPrice: 32.2, wantTier: "base"},
		{model: "doubao-seedance-2.0", resolution: "720p", videoInput: "video", wantPrice: 19.6, wantTier: "video"},
		{model: "doubao-seedance-2.0", resolution: "1080p", videoInput: "none", wantPrice: 35.7, wantTier: "1080p"},
		{model: "doubao-seedance-2.0", resolution: "1080p", videoInput: "video", wantPrice: 21.7, wantTier: "1080p_video"},
		{model: "doubao-seedance-2.0", resolution: "4k", videoInput: "none", wantPrice: 18.2, wantTier: "4k"},
		{model: "doubao-seedance-2.0", resolution: "4k", videoInput: "video", wantPrice: 11.2, wantTier: "4k_video"},
		{model: "doubao-seedance-2.0-fast", resolution: "720p", videoInput: "none", wantPrice: 25.9, wantTier: "base"},
		{model: "doubao-seedance-2.0-fast", resolution: "720p", videoInput: "video", wantPrice: 15.4, wantTier: "video"},
		{model: "doubao-seedance-2.0-mini", resolution: "720p", videoInput: "none", wantPrice: 16.1, wantTier: "base"},
		{model: "doubao-seedance-2.0-mini", resolution: "720p", videoInput: "video", wantPrice: 9.8, wantTier: "video"},
		{model: "doubao-seedance-2.5", resolution: "720p", videoInput: "none", wantPrice: 49, wantTier: "base"},
		{model: "doubao-seedance-2.5", resolution: "720p", videoInput: "video", wantPrice: 29.4, wantTier: "video"},
		{model: "doubao-seedance-2.5", resolution: "1080p", videoInput: "none", wantPrice: 53.9, wantTier: "1080p"},
		{model: "doubao-seedance-2.5", resolution: "1080p", videoInput: "video", wantPrice: 32.2, wantTier: "1080p_video"},
	}

	for _, test := range tests {
		t.Run(test.model+"/"+test.resolution+"/"+test.videoInput, func(t *testing.T) {
			expr, ok := GetBillingExpr(test.model)
			require.True(t, ok)
			assert.Equal(t, BillingModeTieredExpr, GetBillingMode(test.model))

			cost, trace, err := billingexpr.RunExprWithRequest(expr, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: map[string]any{
				"tokens":      1_000_000.0,
				"resolution":  test.resolution,
				"video_input": test.videoInput,
			}})
			require.NoError(t, err)
			assert.InDelta(t, test.wantPrice, cost, 0.000001)
			assert.Equal(t, test.wantTier, trace.MatchedTier)
		})
	}
}
