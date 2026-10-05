package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func TestSendBotMonitorWeChat(t *testing.T) {
	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		received, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	encrypted, err := common.EncryptCredential(server.URL)
	require.NoError(t, err)
	robot := &model.BotMonitorRobot{APIURL: encrypted, ConversationID: "R:10872034605494222"}
	if err := sendBotMonitorWeChat(context.Background(), robot, "channel failed"); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Data struct {
			ConversationID string `json:"conversation_id"`
			Content        string `json:"content"`
		} `json:"data"`
		Type   int `json:"type"`
		Client int `json:"client"`
	}
	if err := common.Unmarshal(received, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.ConversationID != "R:10872034605494222" || payload.Data.Content != "channel failed" || payload.Type != 11029 || payload.Client != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestSendBotMonitorWeChatRejectsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed", http.StatusBadGateway)
	}))
	defer server.Close()
	encrypted, err := common.EncryptCredential(server.URL)
	require.NoError(t, err)
	if err := sendBotMonitorWeChat(context.Background(), &model.BotMonitorRobot{APIURL: encrypted, ConversationID: "R:1"}, "test"); err == nil {
		t.Fatal("expected API error")
	}
}
