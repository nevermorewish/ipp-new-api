package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnterpriseAdminRejectsNormalUserAndAllowsEnabledOwner(t *testing.T) {
	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open("file:enterprise-auth?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Enterprise{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "normal", AffCode: "normal-aff", Type: 0, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.User{Id: 2, Username: "owner", AffCode: "owner-aff", Type: 1, EnterpriseId: 1, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Enterprise{Id: 1, Name: "team", OwnerUserId: 2, Status: model.EnterpriseStatusEnabled}).Error)

	gin.SetMode(gin.TestMode)
	request := func(userId int) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set("id", userId)
			c.Next()
		})
		router.GET("/enterprise", EnterpriseAdmin(), func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		})
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/enterprise", nil))
		return recorder
	}

	require.Equal(t, http.StatusForbidden, request(1).Code)
	require.Equal(t, http.StatusNoContent, request(2).Code)
}
