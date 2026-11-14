package claude

import "github.com/QuantumNous/new-api/relaykit/dto"

// hasLegacyClaudeUsageSemantics reports untagged Claude-derived usage: it
// carries the Claude cache TTL split and reports ordinary input in
// prompt_tokens. Its size relative to the cache counts cannot distinguish
// ordinary input from a total, so it is never reduced by them; upstreams that
// report a total with these fields must tag OpenAI semantics explicitly.
func hasLegacyClaudeUsageSemantics(usage *dto.Usage) bool {
	if usage.UsageSource != "" || usage.UsageSemantic != "" {
		return false
	}
	return usage.ClaudeCacheCreation5mTokens > 0 || usage.ClaudeCacheCreation1hTokens > 0
}

func UsageFromOpenAI(usage *dto.Usage) *dto.ClaudeUsage {
	if usage == nil {
		return nil
	}
	// An existing sidecar snapshots the original provider usage; carry it
	// across this bridge unchanged regardless of its dialect. Only synthesize
	// an OpenAI snapshot when no sidecar exists yet.
	existingBillingUsage := dto.CloneBillingUsage(usage.BillingUsage)
	if existingBillingUsage != nil && existingBillingUsage.ClaudeUsage != nil &&
		(existingBillingUsage.Source == dto.BillingUsageSourceClaudeMessages || existingBillingUsage.Semantic == dto.BillingUsageSemanticAnthropic) {
		result := existingBillingUsage.ClaudeUsage
		result.BillingUsage = dto.CloneBillingUsage(usage.BillingUsage)
		return result
	}
	billingUsage := existingBillingUsage
	if billingUsage == nil {
		billingUsage = dto.NewOpenAIChatBillingUsage(usage)
	}
	// Split every reported cache write, including OpenAI's cache_write_tokens,
	// so the Claude response breaks down the same total it reports. Moonshot
	// reports the 5m/1h split under cache_write_tokens_details, so that field
	// wins over the scalar TTL counters when it carries anything.
	tokens5m, tokens1h := usage.ClaudeCacheCreation5mTokens, usage.ClaudeCacheCreation1hTokens
	if details := usage.PromptTokensDetails.CacheWriteTokensDetails; details != nil &&
		(details.TTL5mTokens > 0 || details.TTL1hTokens > 0) {
		tokens5m, tokens1h = details.TTL5mTokens, details.TTL1hTokens
	}
	cacheCreation5m, cacheCreation1h := NormalizeCacheCreationSplit(
		usage.PromptTokensDetails.CacheCreationTokensTotal(),
		tokens5m,
		tokens1h,
	)
	cacheCreationTokens := max(usage.PromptTokensDetails.CacheCreationTokensTotal(), cacheCreation5m+cacheCreation1h)
	inputTokens := usage.PromptTokens
	if usage.UsageSemantic != dto.BillingUsageSemanticAnthropic && !hasLegacyClaudeUsageSemantics(usage) {
		// OpenAI-style prompt/input totals include cache reads and writes, while
		// Claude reports both separately from input_tokens.
		inputTokens = usage.PromptTokens - usage.PromptTokensDetails.CachedTokens - cacheCreationTokens
		if inputTokens < 0 {
			inputTokens = 0
		}
	}
	result := &dto.ClaudeUsage{
		InputTokens:              inputTokens,
		OutputTokens:             usage.CompletionTokens,
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     usage.PromptTokensDetails.CachedTokens,
		BillingUsage:             billingUsage,
	}
	// Report the split unconditionally: message_start and message_delta both
	// carry this object, and a client matching the two shapes reads an absent
	// lifetime as unreported rather than zero. The flat counters are the same
	// information, so they are kept in sync here exactly as the merge does;
	// otherwise a non-stream response and a merged stream disagree.
	result.CacheCreation = &dto.ClaudeCacheCreationUsage{
		Ephemeral5mInputTokens: cacheCreation5m,
		Ephemeral1hInputTokens: cacheCreation1h,
	}
	result.ClaudeCacheCreation5mTokens = cacheCreation5m
	result.ClaudeCacheCreation1hTokens = cacheCreation1h
	return result
}
