package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
	"os"
	"path/filepath"
	"testing"
)

func TestChannelFiltersExcludeOnlyFailedCapabilities(t *testing.T) {
	filters := []dto.ChannelFilter{{Kind: dto.FilterExcludedChannels, ExcludedChannelIDs: []int{7}}}
	accepted, kind := ChannelSatisfiesFilters(&Channel{Id: 7}, "gpt-6", filters)
	require.False(t, accepted)
	require.Equal(t, dto.FilterExcludedChannels, kind)
	accepted, _ = ChannelSatisfiesFilters(&Channel{Id: 8}, "gpt-6", filters)
	require.True(t, accepted)
	accepted, _ = ChannelSatisfiesFilters(&Channel{Id: 7}, "gpt-6", nil)
	require.True(t, accepted)
}

func TestOpenAIGPTChannelSelectionDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			versionQuery := "SELECT sqlite_version()"
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "channels.db"))
			case "mysql":
				dsn := os.Getenv("OPENAIGPT_TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("OPENAIGPT_TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
				versionQuery = "SELECT version()"
			case "postgres":
				dsn := os.Getenv("OPENAIGPT_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("OPENAIGPT_TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
				versionQuery = "SELECT version()"
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "openaigpt_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			defer sqlDB.Close()
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Log(version)
			require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
			defer db.Migrator().DropTable(&Ability{}, &Channel{})
			originalDB, originalGroupCol, originalCache := DB, commonGroupCol, common.MemoryCacheEnabled
			originalChannels, originalGroups := channelsIDM, group2model2channels
			t.Cleanup(func() {
				DB, commonGroupCol, common.MemoryCacheEnabled = originalDB, originalGroupCol, originalCache
				channelsIDM, group2model2channels = originalChannels, originalGroups
			})
			DB = db
			commonGroupCol = "`group`"
			if dialect == "postgres" {
				commonGroupCol = `"group"`
			}
			channels := []*Channel{
				{Id: 7, Type: constant.ChannelTypeOpenAIGPT, Key: "test-key", Name: "GPT", Status: common.ChannelStatusEnabled},
				{Id: 8, Type: constant.ChannelTypeOpenAI, Key: "test-key", Name: "fallback", Status: common.ChannelStatusEnabled},
			}
			channels[0].SetOtherSettings(kitdto.ChannelOtherSettings{RemoveAzureGPTEncryption: true, RemoveGPTTemperature: true})
			require.NoError(t, db.Create(&channels).Error)
			require.NoError(t, db.Create(&[]Ability{{Group: "default", Model: "gpt-6", ChannelId: 7, Enabled: true}, {Group: "default", Model: "gpt-6", ChannelId: 8, Enabled: true}}).Error)
			var reloaded Channel
			require.NoError(t, db.First(&reloaded, 7).Error)
			require.True(t, reloaded.GetOtherSettings().RemoveAzureGPTEncryption)
			require.True(t, reloaded.GetOtherSettings().RemoveGPTTemperature)
			channelsIDM = map[int]*Channel{7: channels[0], 8: channels[1]}
			group2model2channels = map[string]map[string][]int{"default": {"gpt-6": {7, 8}}}
			for _, cache := range []bool{false, true} {
				common.MemoryCacheEnabled = cache
				filters := []dto.ChannelFilter{{Kind: dto.FilterExcludedChannels, ExcludedChannelIDs: []int{7}}}
				selected, err := GetRandomSatisfiedChannel("default", "gpt-6", 1, filters)
				require.NoError(t, err)
				require.NotNil(t, selected)
				require.Equal(t, 8, selected.Id)
				filters[0].ExcludedChannelIDs = []int{7, 8}
				selected, err = GetRandomSatisfiedChannel("default", "gpt-6", 2, filters)
				require.NoError(t, err)
				require.Nil(t, selected)
			}
		})
	}
}
