package controller

import "testing"

func TestValidateBotMonitorWeChatAPIURL(t *testing.T) {
	valid := []string{
		"http://39.97.229.118:8060/api/Client/ExecCommand",
		"https://wechat.example.com/api/Client/ExecCommand?tenant=1",
	}
	for _, value := range valid {
		if err := validateBotMonitorWeChatAPIURL(value); err != nil {
			t.Fatalf("valid API URL rejected: %s: %v", value, err)
		}
	}
	invalid := []string{
		"ftp://39.97.229.118/api/Client/ExecCommand",
		"/api/Client/ExecCommand",
		"https://user:pass@example.com/api/Client/ExecCommand",
		"https://example.com/api/Client/ExecCommand#fragment",
	}
	for _, value := range invalid {
		if err := validateBotMonitorWeChatAPIURL(value); err == nil {
			t.Errorf("invalid API URL accepted: %s", value)
		}
	}
}
