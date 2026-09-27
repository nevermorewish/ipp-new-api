// Package embed implements the SeedanceAPI signed tenant identity protocol.
package embed

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

const (
	HeaderContext   = "X-Embed-Ctx"
	HeaderSignature = "X-Embed-Sig"
)

// Identity contains no usable API credential. It can be retained in private
// task state so polling does not depend on the lifetime of the client token.
type Identity struct {
	TokenID   int    `json:"tid"`
	KeyHash   string `json:"kh"`
	UserID    int    `json:"uid,omitempty"`
	Group     string `json:"group,omitempty"`
	RequestID string `json:"rid,omitempty"`
}

func FromContext(c *gin.Context) Identity {
	key := common.GetContextKeyString(c, constant.ContextKeyTokenKey)
	identity := Identity{
		TokenID:   common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		UserID:    common.GetContextKeyInt(c, constant.ContextKeyUserId),
		Group:     common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		RequestID: c.GetString(common.RequestIdKey),
	}
	if key != "" {
		sum := sha256.Sum256([]byte(key))
		identity.KeyHash = hex.EncodeToString(sum[:])
	}
	return identity
}

// SignedHeaders issues the upstream's 60-second identity assertion. The
// receiver must verify its HMAC and expiry before authorizing the tenant.
func SignedHeaders(identity Identity, secret string) (map[string]string, error) {
	hash, err := hex.DecodeString(identity.KeyHash)
	if identity.TokenID <= 0 || err != nil || len(hash) != sha256.Size || strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("SeedanceAPI requires an authenticated token and a channel signing secret")
	}
	payload, err := common.Marshal(struct {
		Identity
		Expires int64 `json:"exp"`
	}{Identity: identity, Expires: time.Now().Add(time.Minute).Unix()})
	if err != nil {
		return nil, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encoded))
	return map[string]string{HeaderContext: encoded, HeaderSignature: hex.EncodeToString(mac.Sum(nil))}, nil
}
