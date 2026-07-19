package middleware

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokenModelLimitAllowsResolvedEnterpriseAlias(t *testing.T) {
	const (
		requestedModel = "corp-chat"
		canonicalModel = "real-model"
	)

	t.Run("alias-only whitelist", func(t *testing.T) {
		allowed, displayModel := tokenModelLimitAllows(
			map[string]bool{requestedModel: true},
			canonicalModel,
			requestedModel,
		)

		require.True(t, allowed)
		require.Equal(t, requestedModel, displayModel)
	})

	t.Run("canonical whitelist", func(t *testing.T) {
		allowed, displayModel := tokenModelLimitAllows(
			map[string]bool{canonicalModel: true},
			canonicalModel,
			requestedModel,
		)

		require.True(t, allowed)
		require.Equal(t, requestedModel, displayModel)
	})
}

func TestTokenModelLimitRejectsDirectCanonicalRequestWithAliasOnlyWhitelist(t *testing.T) {
	allowed, displayModel := tokenModelLimitAllows(
		map[string]bool{"corp-chat": true},
		"real-model",
		"",
	)

	require.False(t, allowed)
	require.Equal(t, "real-model", displayModel)
}

func TestTokenModelLimitPreservesExistingMatchingRulesForResolvedAliases(t *testing.T) {
	t.Run("compact alias whitelist uses client-facing base model", func(t *testing.T) {
		allowed, displayModel := tokenModelLimitAllows(
			map[string]bool{"corp-chat": true},
			"real-model-openai-compact",
			"corp-chat",
		)

		require.True(t, allowed)
		require.Equal(t, "corp-chat", displayModel)
	})

	t.Run("compact canonical whitelist keeps compact suffix", func(t *testing.T) {
		compactCanonical := "real-model-openai-compact"
		allowed, _ := tokenModelLimitAllows(
			map[string]bool{compactCanonical: true},
			compactCanonical,
			"corp-chat",
		)

		require.True(t, allowed)
	})

	t.Run("compact canonical does not match unsuffixed whitelist", func(t *testing.T) {
		allowed, _ := tokenModelLimitAllows(
			map[string]bool{"real-model": true},
			"real-model-openai-compact",
			"corp-chat",
		)

		require.False(t, allowed)
	})

	t.Run("existing formatted wildcard remains supported", func(t *testing.T) {
		allowed, _ := tokenModelLimitAllows(
			map[string]bool{"gpt-4-gizmo-*": true},
			"gpt-4-gizmo-corp",
			"",
		)

		require.True(t, allowed)
	})
}
