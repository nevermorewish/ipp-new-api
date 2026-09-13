package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

func TestSeedanceExposesOpenAIVideoEndpoint(t *testing.T) {
	endpointTypes := GetEndpointTypesByChannelType(constant.ChannelTypeSeedance, "doubao-seedance-2.0")

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAIVideo}, endpointTypes)
}
