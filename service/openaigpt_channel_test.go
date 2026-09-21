package service

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOpenAIGPTCapabilityMismatchDoesNotDisableChannel(t *testing.T) {
	original := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = original })
	apiErr := types.NewErrorWithStatusCode(errors.New("unsupported tool"), types.ErrorCodeChannelOpenAIResponsesUnsupported, 400)
	require.False(t, ShouldDisableChannel(apiErr))
}
