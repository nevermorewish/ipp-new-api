package relay

import (
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/types"
)

func newOpenAIGPTContractError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(
		err,
		types.ErrorCodeInvalidRequest,
		http.StatusBadRequest,
		types.ErrOptionWithSkipRetry(),
	)
}
