package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnterpriseModelAliasesAreIsolatedByOwner(t *testing.T) {
	db := newEnterpriseModelAliasTestDB(t)
	createEnterpriseAliasAbility(t, db, "upstream-a")
	createEnterpriseAliasAbility(t, db, "upstream-b")

	first, err := UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_owner_a",
		Alias:           "corp-chat",
		UpstreamModelID: "upstream-a",
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, first.Version)

	second, err := UpsertEnterpriseModelAlias(202, EnterpriseModelAliasMutation{
		SourceID:        "mdl_owner_b",
		Alias:           "corp-chat",
		UpstreamModelID: "upstream-b",
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, second.Version)

	firstResolution, found, err := ResolveEnterpriseModelAlias(101, "corp-chat")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, EnterpriseModelAliasStatusActive, firstResolution.Status)
	require.Equal(t, "upstream-a", firstResolution.UpstreamModelID)

	secondResolution, found, err := ResolveEnterpriseModelAlias(202, "corp-chat")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "upstream-b", secondResolution.UpstreamModelID)
}

func TestEnterpriseModelAliasUpsertIsIdempotentAndImmutable(t *testing.T) {
	db := newEnterpriseModelAliasTestDB(t)
	createEnterpriseAliasAbility(t, db, "upstream-a")
	createEnterpriseAliasAbility(t, db, "upstream-b")

	created, err := UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_stable",
		Alias:           "corp-chat",
		UpstreamModelID: "upstream-a",
	})
	require.NoError(t, err)

	retried, err := UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_stable",
		Alias:           "corp-chat",
		UpstreamModelID: "upstream-a",
		ExpectedVersion: uint64Pointer(0),
	})
	require.NoError(t, err)
	require.Equal(t, created.ID, retried.ID)
	require.Equal(t, created.Version, retried.Version)

	_, err = UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_stable",
		Alias:           "corp-chat",
		UpstreamModelID: "upstream-b",
		ExpectedVersion: uint64Pointer(created.Version),
	})
	require.ErrorIs(t, err, ErrEnterpriseModelAliasImmutable)

	_, err = UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_other_source",
		Alias:           "corp-chat",
		UpstreamModelID: "upstream-a",
	})
	require.ErrorIs(t, err, ErrEnterpriseModelAliasConflict)

	row, found, err := ResolveEnterpriseModelAlias(101, "corp-chat")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "upstream-a", row.UpstreamModelID)
	require.EqualValues(t, 1, countEnterpriseModelAliasRows(t, db))
}

func TestEnterpriseModelAliasRejectsUnsafeOrUnroutableModels(t *testing.T) {
	db := newEnterpriseModelAliasTestDB(t)
	createEnterpriseAliasAbility(t, db, "canonical-model")
	createEnterpriseAliasAbility(t, db, "occupied-alias")

	cases := []struct {
		name     string
		mutation EnterpriseModelAliasMutation
		target   error
	}{
		{name: "missing source", mutation: EnterpriseModelAliasMutation{Alias: "corp-chat", UpstreamModelID: "canonical-model"}, target: ErrEnterpriseModelAliasInvalid},
		{name: "reserved prefix", mutation: EnterpriseModelAliasMutation{SourceID: "mdl_1", Alias: "custom-local:corp-chat", UpstreamModelID: "canonical-model"}, target: ErrEnterpriseModelAliasInvalid},
		{name: "reserved compact suffix", mutation: EnterpriseModelAliasMutation{SourceID: "mdl_1", Alias: "corp-chat-openai-compact", UpstreamModelID: "canonical-model"}, target: ErrEnterpriseModelAliasInvalid},
		{name: "comma", mutation: EnterpriseModelAliasMutation{SourceID: "mdl_1", Alias: "corp,chat", UpstreamModelID: "canonical-model"}, target: ErrEnterpriseModelAliasInvalid},
		{name: "control", mutation: EnterpriseModelAliasMutation{SourceID: "mdl_1", Alias: "corp\nchat", UpstreamModelID: "canonical-model"}, target: ErrEnterpriseModelAliasInvalid},
		{name: "same model", mutation: EnterpriseModelAliasMutation{SourceID: "mdl_1", Alias: "canonical-model", UpstreamModelID: "canonical-model"}, target: ErrEnterpriseModelAliasInvalid},
		{name: "unsupported upstream", mutation: EnterpriseModelAliasMutation{SourceID: "mdl_1", Alias: "corp-chat", UpstreamModelID: "missing-model"}, target: ErrEnterpriseModelAliasUnsupported},
		{name: "canonical collision", mutation: EnterpriseModelAliasMutation{SourceID: "mdl_1", Alias: "occupied-alias", UpstreamModelID: "canonical-model"}, target: ErrEnterpriseModelAliasConflict},
		{name: "long alias", mutation: EnterpriseModelAliasMutation{SourceID: "mdl_1", Alias: strings.Repeat("a", 129), UpstreamModelID: "canonical-model"}, target: ErrEnterpriseModelAliasInvalid},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := UpsertEnterpriseModelAlias(101, testCase.mutation)
			require.ErrorIs(t, err, testCase.target)
		})
	}
	require.Zero(t, countEnterpriseModelAliasRows(t, db))
}

func TestEnterpriseModelAliasCannotChainThroughAnotherAlias(t *testing.T) {
	db := newEnterpriseModelAliasTestDB(t)
	createEnterpriseAliasAbility(t, db, "real-model")
	_, err := UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_first",
		Alias:           "corp-first",
		UpstreamModelID: "real-model",
	})
	require.NoError(t, err)

	_, err = UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_second",
		Alias:           "corp-second",
		UpstreamModelID: "corp-first",
	})
	require.ErrorIs(t, err, ErrEnterpriseModelAliasUnsupported)
}

func TestEnterpriseModelAliasTombstoneIsOwnedVersionedAndFailClosed(t *testing.T) {
	db := newEnterpriseModelAliasTestDB(t)
	createEnterpriseAliasAbility(t, db, "real-model")
	created, err := UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_delete",
		Alias:           "corp-chat",
		UpstreamModelID: "real-model",
	})
	require.NoError(t, err)
	_, err = TombstoneEnterpriseModelAlias(101, "mdl_delete", nil)
	require.ErrorIs(t, err, ErrEnterpriseModelAliasVersionConflict)

	_, err = TombstoneEnterpriseModelAlias(202, "mdl_delete", uint64Pointer(created.Version))
	require.ErrorIs(t, err, ErrEnterpriseModelAliasForbidden)
	_, err = TombstoneEnterpriseModelAlias(101, "mdl_delete", uint64Pointer(created.Version+1))
	require.ErrorIs(t, err, ErrEnterpriseModelAliasVersionConflict)

	deleted, err := TombstoneEnterpriseModelAlias(101, "mdl_delete", uint64Pointer(created.Version))
	require.NoError(t, err)
	require.Equal(t, EnterpriseModelAliasStatusTombstone, deleted.Status)
	require.EqualValues(t, 2, deleted.Version)

	retried, err := TombstoneEnterpriseModelAlias(101, "mdl_delete", uint64Pointer(created.Version))
	require.NoError(t, err)
	require.Equal(t, deleted.Version, retried.Version)

	resolution, found, err := ResolveEnterpriseModelAlias(101, "corp-chat")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, EnterpriseModelAliasStatusTombstone, resolution.Status)

	_, err = UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_reuse",
		Alias:           "corp-chat",
		UpstreamModelID: "real-model",
	})
	require.ErrorIs(t, err, ErrEnterpriseModelAliasConflict)
	require.EqualValues(t, 1, countEnterpriseModelAliasRows(t, db))
}

func TestEnterpriseModelAliasCanBeDisabledAndReenabledWithVersionChecks(t *testing.T) {
	db := newEnterpriseModelAliasTestDB(t)
	createEnterpriseAliasAbility(t, db, "real-model")
	created, err := UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_toggle",
		Alias:           "corp-chat",
		UpstreamModelID: "real-model",
		Enabled:         boolPointer(true),
	})
	require.NoError(t, err)

	_, err = UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_toggle",
		Alias:           "corp-chat",
		UpstreamModelID: "real-model",
		Enabled:         boolPointer(false),
		ExpectedVersion: uint64Pointer(created.Version + 1),
	})
	require.ErrorIs(t, err, ErrEnterpriseModelAliasVersionConflict)

	disabled, err := UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_toggle",
		Alias:           "corp-chat",
		UpstreamModelID: "real-model",
		Enabled:         boolPointer(false),
		ExpectedVersion: uint64Pointer(created.Version),
	})
	require.NoError(t, err)
	require.Equal(t, EnterpriseModelAliasStatusDisabled, disabled.Status)
	require.EqualValues(t, 2, disabled.Version)

	retried, err := UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_toggle",
		Alias:           "corp-chat",
		UpstreamModelID: "real-model",
		Enabled:         boolPointer(false),
		ExpectedVersion: uint64Pointer(created.Version),
	})
	require.NoError(t, err)
	require.Equal(t, disabled.Version, retried.Version)

	reenabled, err := UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_toggle",
		Alias:           "corp-chat",
		UpstreamModelID: "real-model",
		Enabled:         boolPointer(true),
		ExpectedVersion: uint64Pointer(disabled.Version),
	})
	require.NoError(t, err)
	require.Equal(t, EnterpriseModelAliasStatusActive, reenabled.Status)
	require.EqualValues(t, 3, reenabled.Version)
}

func TestGetEnterpriseModelAliasBySourceIsTenantScopedAndReturnsTombstones(t *testing.T) {
	db := newEnterpriseModelAliasTestDB(t)
	createEnterpriseAliasAbility(t, db, "real-model")
	created, err := UpsertEnterpriseModelAlias(101, EnterpriseModelAliasMutation{
		SourceID:        "mdl_reconcile",
		Alias:           "corp-chat",
		UpstreamModelID: "real-model",
	})
	require.NoError(t, err)

	current, err := GetEnterpriseModelAliasBySource(101, created.SourceID)
	require.NoError(t, err)
	require.Equal(t, created, current)

	_, err = GetEnterpriseModelAliasBySource(202, created.SourceID)
	require.ErrorIs(t, err, ErrEnterpriseModelAliasNotFound)
	_, err = GetEnterpriseModelAliasBySource(101, "mdl_never_created")
	require.ErrorIs(t, err, ErrEnterpriseModelAliasNotFound)

	tombstone, err := TombstoneEnterpriseModelAlias(101, created.SourceID, uint64Pointer(created.Version))
	require.NoError(t, err)
	current, err = GetEnterpriseModelAliasBySource(101, created.SourceID)
	require.NoError(t, err)
	require.Equal(t, EnterpriseModelAliasStatusTombstone, current.Status)
	require.Equal(t, tombstone.Version, current.Version)

	_, err = GetEnterpriseModelAliasBySource(0, created.SourceID)
	require.ErrorIs(t, err, ErrEnterpriseModelAliasInvalid)
	_, err = GetEnterpriseModelAliasBySource(101, "../unsafe")
	require.ErrorIs(t, err, ErrEnterpriseModelAliasInvalid)
}

func newEnterpriseModelAliasTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	previousLogDB := LOG_DB
	dsn := fmt.Sprintf("file:enterprise-model-alias-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	LOG_DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&Ability{}, &EnterpriseModelAlias{}))
	t.Cleanup(func() {
		DB = previousDB
		LOG_DB = previousLogDB
	})
	return db
}

func createEnterpriseAliasAbility(t *testing.T, db *gorm.DB, modelName string) {
	t.Helper()
	require.NoError(t, db.Create(&Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: len(modelName) + 100,
		Enabled:   true,
	}).Error)
}

func countEnterpriseModelAliasRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&EnterpriseModelAlias{}).Count(&count).Error)
	return count
}

func uint64Pointer(value uint64) *uint64 {
	return &value
}

func boolPointer(value bool) *bool {
	return &value
}
