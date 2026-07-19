package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func DisableCache() gin.HandlerFunc {
	return func(c *gin.Context) {
		common.SetNoStoreHeaders(c)
		c.Next()
	}
}
