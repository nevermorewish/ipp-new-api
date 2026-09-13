package constant

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetChannelBaseURLIsBoundsSafe(t *testing.T) {
	assert.Empty(t, GetChannelBaseURL(ChannelTypeTaskPlugin))
	assert.Equal(t, "https://api.huanxing.ai", GetChannelBaseURL(ChannelTypeSeedance))
	assert.Empty(t, GetChannelBaseURL(9999))
}
