package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDefaultBotMonitorTriggerStatusCodes(t *testing.T) {
	if defaultBotMonitorTriggerStatusCodes != "400-499,500-599" {
		t.Fatalf("default trigger status codes = %q", defaultBotMonitorTriggerStatusCodes)
	}
}

func TestBotMonitorChannelIDsRoundTrip(t *testing.T) {
	encoded, err := EncodeBotMonitorChannelIds([]int{4, 2, 4, -1, 1})
	require.NoError(t, err)
	if encoded != "[1,2,4]" {
		t.Fatalf("encoded = %s", encoded)
	}
	decoded, err := DecodeBotMonitorChannelIds(encoded)
	require.NoError(t, err)
	if len(decoded) != 3 || decoded[0] != 1 || decoded[1] != 2 || decoded[2] != 4 {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestBotMonitorUserIDsRoundTrip(t *testing.T) {
	encoded, err := EncodeBotMonitorUserIds([]int{8, 3, 8, 0, -1, 5})
	require.NoError(t, err)
	if encoded != "[3,5,8]" {
		t.Fatalf("encoded = %s", encoded)
	}
	decoded, err := DecodeBotMonitorUserIds(encoded)
	require.NoError(t, err)
	if len(decoded) != 3 || decoded[0] != 3 || decoded[1] != 5 || decoded[2] != 8 {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestDefaultBotMonitorBalanceThreshold(t *testing.T) {
	if DefaultBotMonitorBalanceThresholdAmount != 5000 {
		t.Fatalf("default balance threshold amount = %f", DefaultBotMonitorBalanceThresholdAmount)
	}
	if DefaultBotMonitorCheckIntervalHours != 1 {
		t.Fatalf("default balance check interval = %f", DefaultBotMonitorCheckIntervalHours)
	}
}

func TestBotMonitorModelNamesRoundTrip(t *testing.T) {
	encoded, err := EncodeBotMonitorModelNames([]string{" gpt-4 ", "claude-3", "gpt-4", ""})
	require.NoError(t, err)
	if encoded != `["claude-3","gpt-4"]` {
		t.Fatalf("encoded = %s", encoded)
	}
	decoded, err := DecodeBotMonitorModelNames(encoded)
	require.NoError(t, err)
	if len(decoded) != 2 || decoded[0] != "claude-3" || decoded[1] != "gpt-4" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestBotMonitorLatencyBindingDefaults(t *testing.T) {
	binding := &BotMonitorLatencyBinding{}
	if err := binding.BeforeCreate(nil); err != nil {
		t.Fatal(err)
	}
	if binding.FirstTokenTimeoutSeconds != DefaultBotMonitorFirstTokenTimeoutSeconds {
		t.Fatalf("timeout = %d", binding.FirstTokenTimeoutSeconds)
	}
}
