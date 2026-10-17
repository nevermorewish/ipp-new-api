package billing_setting

// Built-in token prices use actual USD per million tokens. Keep new model
// defaults here instead of splitting them across the legacy ratio tables.
var builtinBillingExpr = map[string]string{
	// https://developers.openai.com/api/docs/pricing (Standard, 2026-09-09).
	// The Images API reports image output in output_tokens, normalized to c.
	"gpt-image-2":            `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`,
	"gpt-image-2.5-sunburst": `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`,
	"gpt-image-2.5-flare":    `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`,
	// https://developers.openai.com/api/docs/models/gpt-6-astra
	// Standard pricing; the long-context rates apply to the whole request.
	// Do not infer service-tier discounts from incoming request parameters:
	// channels filter service_tier by default, so it may not reach the upstream.
	"gpt-6-astra": `len <= 272000 ? tier("standard", p * 10 + c * 50 + cr * 1 + cc * 12.5) : tier("long_context", p * 20 + c * 75 + cr * 2 + cc * 25)`,
	// Restore the custom Seedance task tariffs from before the upstream merge.
	// These are historical site tariffs, not a refresh of provider list prices.
	"doubao-seedance-2.0":      `u("resolution") == "1080p" && u("video_input") == "video" ? tier("1080p_video", u("tokens") * 21.7 / 1000000) : u("resolution") == "1080p" ? tier("1080p", u("tokens") * 35.7 / 1000000) : u("resolution") == "4k" && u("video_input") == "video" ? tier("4k_video", u("tokens") * 11.2 / 1000000) : u("resolution") == "4k" ? tier("4k", u("tokens") * 18.2 / 1000000) : u("video_input") == "video" ? tier("video", u("tokens") * 19.6 / 1000000) : tier("base", u("tokens") * 32.2 / 1000000)`,
	"doubao-seedance-2.0-fast": `u("video_input") == "video" ? tier("video", u("tokens") * 15.4 / 1000000) : tier("base", u("tokens") * 25.9 / 1000000)`,
	"doubao-seedance-2.0-mini": `u("video_input") == "video" ? tier("video", u("tokens") * 9.8 / 1000000) : tier("base", u("tokens") * 16.1 / 1000000)`,
	"doubao-seedance-2.5":      `u("resolution") == "1080p" && u("video_input") == "video" ? tier("1080p_video", u("tokens") * 32.2 / 1000000) : u("resolution") == "1080p" ? tier("1080p", u("tokens") * 53.9 / 1000000) : u("video_input") == "video" ? tier("video", u("tokens") * 29.4 / 1000000) : tier("base", u("tokens") * 49 / 1000000)`,
}
