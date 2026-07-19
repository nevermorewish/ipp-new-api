package middleware

import "github.com/QuantumNous/new-api/setting/ratio_setting"

// tokenModelLimitAllows checks both the client-facing enterprise alias and the
// canonical routing model. The display name remains client-facing, while the
// caller continues to use canonicalModel for channel selection and billing.
func tokenModelLimitAllows(modelLimit map[string]bool, canonicalModel string, requestedModel string) (bool, string) {
	displayModel := canonicalModel
	if requestedModel != "" {
		displayModel = requestedModel
		if _, ok := modelLimit[ratio_setting.FormatMatchingModelName(requestedModel)]; ok {
			return true, displayModel
		}
	}

	_, ok := modelLimit[ratio_setting.FormatMatchingModelName(canonicalModel)]
	return ok, displayModel
}
