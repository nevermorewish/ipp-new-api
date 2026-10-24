package billing_setting

import "github.com/QuantumNous/new-api/constant"

// deepSeekPeak is DeepSeek's peak window: Monday to Friday, 09:00-12:00 and
// 14:00-18:00 Beijing time. The Chinese public holidays DeepSeek bills
// off-peak are not known in advance and bill at peak here.
const deepSeekPeak = `weekday("Asia/Shanghai") >= 1 && weekday("Asia/Shanghai") <= 5 && ((hour("Asia/Shanghai") >= 9 && hour("Asia/Shanghai") < 12) || (hour("Asia/Shanghai") >= 14 && hour("Asia/Shanghai") < 18))`

const deepSeekFlashExpr = deepSeekPeak + ` ? tier("peak", p * 0.3 + cr * 0.006 + c * 1.2) : tier("off_peak", p * 0.15 + cr * 0.003 + c * 0.6)`

// Built-in token prices use actual USD per million tokens. Keep new model
// defaults here instead of splitting them across the legacy ratio tables.
var builtinBillingExpr = map[string]string{
	// https://developers.openai.com/api/docs/pricing (Standard, 2026-09-09).
	// The Images API reports image output in output_tokens, normalized to c.
	"gpt-image-2":            `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`,
	"gpt-image-2.5-sunburst": `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`,
	"gpt-image-2.5-flare":    `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`,
	// https://developers.openai.com/api/docs/models/gpt-6-astra
	// https://developers.openai.com/api/docs/pricing (Standard, 2026-10-22):
	// cc is the 30m cache write and cc12h the 12h write. The long-context
	// rates apply to the whole request.
	// Do not infer service-tier discounts from incoming request parameters:
	// channels filter service_tier by default, so it may not reach the upstream.
	"gpt-6-astra": `len <= 272000 ? tier("standard", p * 10 + c * 50 + cr * 1 + cc * 12.5 + cc12h * 25) : tier("long_context", p * 20 + c * 75 + cr * 2 + cc * 25 + cc12h * 50)`,
	// https://api-docs.deepseek.com/quick_start/pricing (2026-10-17). Off-peak
	// is half the peak price. deepseek-v4-flash and deepseek-v4-flash-vision-exp
	// run on V4.1 Flash and bill at the Flash price. Images bill as input
	// tokens, and there is no cache-write price.
	"deepseek-flash":               deepSeekFlashExpr,
	"deepseek-v4-flash":            deepSeekFlashExpr,
	"deepseek-v4-flash-vision-exp": deepSeekFlashExpr,
	"deepseek-v4-pro":              deepSeekPeak + ` ? tier("peak", p * 1.32 + cr * 0.044 + c * 3.96) : tier("off_peak", p * 0.66 + cr * 0.022 + c * 1.98)`,
	// The Search and Fetch API routing names: $0.75 per 1,000 successful
	// requests each, the same pricing page. The calls carry no tokens.
	constant.DeepSeekSearchModel: `tier("request", fixed(0.00075))`,
	constant.DeepSeekFetchModel:  `tier("request", fixed(0.00075))`,
	// Restore the custom Seedance task tariffs from before the upstream merge.
	// These are historical site tariffs, not a refresh of provider list prices.
	"doubao-seedance-2.0":      `u("resolution") == "1080p" && u("video_input") == "video" ? tier("1080p_video", u("tokens") * 21.7 / 1000000) : u("resolution") == "1080p" ? tier("1080p", u("tokens") * 35.7 / 1000000) : u("resolution") == "4k" && u("video_input") == "video" ? tier("4k_video", u("tokens") * 11.2 / 1000000) : u("resolution") == "4k" ? tier("4k", u("tokens") * 18.2 / 1000000) : u("video_input") == "video" ? tier("video", u("tokens") * 19.6 / 1000000) : tier("base", u("tokens") * 32.2 / 1000000)`,
	"doubao-seedance-2.0-fast": `u("video_input") == "video" ? tier("video", u("tokens") * 15.4 / 1000000) : tier("base", u("tokens") * 25.9 / 1000000)`,
	"doubao-seedance-2.0-mini": `u("video_input") == "video" ? tier("video", u("tokens") * 9.8 / 1000000) : tier("base", u("tokens") * 16.1 / 1000000)`,
	"doubao-seedance-2.5":      `u("resolution") == "1080p" && u("video_input") == "video" ? tier("1080p_video", u("tokens") * 32.2 / 1000000) : u("resolution") == "1080p" ? tier("1080p", u("tokens") * 53.9 / 1000000) : u("video_input") == "video" ? tier("video", u("tokens") * 29.4 / 1000000) : tier("base", u("tokens") * 49 / 1000000)`,
}
