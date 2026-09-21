package controller

import (
	"errors"
	constraintdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOpenAIGPTCapabilityRetryHonorsBudgetAndPins(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining int
		pin       bool
		want      bool
	}{
		{"another channel can retry", 1, false, true},
		{"exhausted retry budget", 0, false, false},
		{"explicit channel stays pinned", 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(nil)
			if tc.pin {
				service.GetChannelConstraints(ctx).AddPin(constraintdto.ChannelPin{ChannelId: 7})
			}
			apiErr := types.NewErrorWithStatusCode(errors.New("unsupported tool"), types.ErrorCodeChannelOpenAIResponsesUnsupported, 400)
			require.Equal(t, tc.want, shouldRetry(ctx, apiErr, tc.remaining))
		})
	}
}
