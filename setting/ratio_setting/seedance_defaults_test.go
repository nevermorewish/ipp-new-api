package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedanceDefaultsUseOfficialOriginalPricesAtSevenTenths(t *testing.T) {
	tests := []struct {
		model         string
		originalPrice float64
	}{
		{model: "doubao-seedance-2.0", originalPrice: 46},
		{model: "doubao-seedance-2.0-fast", originalPrice: 37},
		{model: "doubao-seedance-2.0-mini", originalPrice: 23},
		{model: "doubao-seedance-2.5", originalPrice: 70},
	}

	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			original, ok := defaultOriginalModelPrice[test.model]
			require.True(t, ok)
			require.NotNil(t, original.Input)
			require.NotNil(t, original.Output)
			assert.Equal(t, test.originalPrice, *original.Input)
			assert.Equal(t, test.originalPrice, *original.Output)

			ratio, ok := defaultModelRatio[test.model]
			require.True(t, ok)
			assert.InDelta(t, 0.7, ratio*2/test.originalPrice, 1e-12)
		})
	}
}
