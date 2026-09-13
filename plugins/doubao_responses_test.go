package plugins_test

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoubaoResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "doubao",
		model:     "doubao-seedance-2-0-260128",
		requestBody: map[string]any{
			"model": "doubao-seedance-2-0-260128",
			"input": []any{map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "a running fox"},
				map[string]any{"type": "input_image", "image_url": "https://cdn.example/frame.png"},
			}}},
			"seconds": 6,
			"size":    "1920x1080",
		},
		wantAction: "image_to_video",
		wantRequest: map[string]any{
			"model":   "doubao-seedance-2-0-260128",
			"prompt":  "a running fox",
			"images":  []any{"https://cdn.example/frame.png"},
			"seconds": float64(6),
			"metadata": map[string]any{
				"resolution": "1080p",
			},
		},
		wantUsageKeys:  []string{"resolution", "tokens", "video_input"},
		wantVendorName: "doubao",
	})
}

func TestDoubaoSeedanceAliasUsageIncludesReferenceVideo(t *testing.T) {
	plugin, found := jsplugin.DefaultRegistry.Get("doubao")
	require.True(t, found)

	value, err := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{
		"model":         "doubao-seedance-2.0",
		"upstreamModel": "doubao-seedance-2.0",
		"usagePurpose":  "facts",
		"requestBody": map[string]any{
			"model":   "doubao-seedance-2.0",
			"seconds": float64(10),
			"metadata": map[string]any{
				"resolution":          "720p",
				"input_video_seconds": float64(7),
				"content": []any{map[string]any{
					"type":      "video_url",
					"video_url": map[string]any{"url": "https://cdn.example/reference.mp4"},
				}},
			},
		},
	})
	require.NoError(t, err)
	usage, ok := value.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, int64(367200), usage["tokens"])
	assert.Equal(t, "720p", usage["resolution"])
	assert.Equal(t, "video", usage["video_input"])
}

func TestDoubaoSeedanceAliasUsageUsesModelDefaultsAndTokenRates(t *testing.T) {
	plugin, found := jsplugin.DefaultRegistry.Get("doubao")
	require.True(t, found)

	tests := []struct {
		name       string
		model      string
		request    map[string]any
		wantTokens float64
		wantRes    string
	}{
		{
			name:       "2.0 defaults to four seconds at 720p",
			model:      "doubao-seedance-2.0",
			request:    map[string]any{"model": "doubao-seedance-2.0", "metadata": map[string]any{}},
			wantTokens: 86400,
			wantRes:    "720p",
		},
		{
			name:       "2.0 uses its official 480p token rate",
			model:      "doubao-seedance-2.0",
			request:    map[string]any{"model": "doubao-seedance-2.0", "seconds": 5, "metadata": map[string]any{"resolution": "480p"}},
			wantTokens: 50220,
			wantRes:    "480p",
		},
		{
			name:       "2.5 uses its official 480p token rate",
			model:      "doubao-seedance-2.5",
			request:    map[string]any{"model": "doubao-seedance-2.5", "seconds": 5, "metadata": map[string]any{"resolution": "480p"}},
			wantTokens: 48037.5,
			wantRes:    "480p",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{
				"model":         test.model,
				"upstreamModel": test.model,
				"usagePurpose":  "facts",
				"requestBody":   test.request,
			})
			require.NoError(t, err)
			usage, ok := value.(map[string]any)
			require.True(t, ok)
			assert.InDelta(t, test.wantTokens, usage["tokens"], 1e-9)
			assert.Equal(t, test.wantRes, usage["resolution"])
		})
	}
}

func TestDoubaoSeedanceAliasRejectsUnsupportedBillingInputs(t *testing.T) {
	plugin, found := jsplugin.DefaultRegistry.Get("doubao")
	require.True(t, found)

	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "unsupported resolution",
			body: map[string]any{
				"model":      "doubao-seedance-2.0-fast",
				"content":    []any{map[string]any{"type": "text", "text": "a cat"}},
				"duration":   5,
				"resolution": "1080p",
			},
		},
		{
			name: "oversized duration",
			body: map[string]any{
				"model":    "doubao-seedance-2.0",
				"content":  []any{map[string]any{"type": "text", "text": "a cat"}},
				"duration": 3601,
			},
		},
		{
			name: "oversized input video duration",
			body: map[string]any{
				"model":               "doubao-seedance-2.0",
				"content":             []any{map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://cdn.example/input.mp4"}}},
				"duration":            5,
				"input_video_seconds": 3601,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := plugin.Engine.CallMember(t.Context(), "native", "createTask", map[string]any{
				"body": map[string]any{"kind": "json", "value": test.body},
			})
			require.Error(t, err)
		})
	}
}
