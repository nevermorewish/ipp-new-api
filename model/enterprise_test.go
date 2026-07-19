package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDisableEnterpriseForOwnerDisablesEveryChild(t *testing.T) {
	previousDB := DB
	previousLogDB := LOG_DB
	db, err := gorm.Open(sqlite.Open("file:enterprise-disable?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	LOG_DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		LOG_DB = previousLogDB
	})

	require.NoError(t, db.AutoMigrate(&User{}, &Enterprise{}, &Token{}))
	owner := User{Id: 1, Username: "owner", AffCode: "owner-aff", Type: 1, Status: common.UserStatusEnabled, EnterpriseId: 1}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&Enterprise{Id: 1, Name: "team", OwnerUserId: owner.Id, Status: EnterpriseStatusEnabled}).Error)

	children := make([]User, 1001)
	for i := range children {
		children[i] = User{
			Id:           i + 2,
			Username:     fmt.Sprintf("child-%d", i),
			AffCode:      fmt.Sprintf("child-aff-%d", i),
			Type:         2,
			Topid:        owner.Id,
			EnterpriseId: 1,
			Status:       common.UserStatusEnabled,
		}
	}
	require.NoError(t, db.CreateInBatches(children, 100).Error)

	ids, err := GetEnterpriseChildUserIds(owner.Id)
	require.NoError(t, err)
	require.Len(t, ids, 1001)
	require.NoError(t, DisableEnterpriseForOwner(owner.Id))

	var enabledChildren int64
	require.NoError(t, db.Model(&User{}).
		Where("type = ? AND status = ?", 2, common.UserStatusEnabled).
		Count(&enabledChildren).Error)
	require.Zero(t, enabledChildren)
	var enterprise Enterprise
	require.NoError(t, db.First(&enterprise, 1).Error)
	require.Equal(t, EnterpriseStatusDisabled, enterprise.Status)
}

func TestInitTaskPersistsBillingUserId(t *testing.T) {
	task := InitTask(constant.TaskPlatformSuno, &relaycommon.RelayInfo{
		UserId:        202,
		BillingUserId: 101,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
		ChannelMeta:   &relaycommon.ChannelMeta{},
	})
	require.Equal(t, 202, task.UserId)
	require.Equal(t, 101, task.PrivateData.BillingUserId)
}
