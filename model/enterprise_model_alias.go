package model

import (
	"errors"
	"regexp"
	"strings"

	"gorm.io/gorm"
)

const (
	EnterpriseModelAliasStatusActive    = 1
	EnterpriseModelAliasStatusDisabled  = 2
	EnterpriseModelAliasStatusTombstone = 3
	enterpriseReservedCompactSuffix     = "-openai-compact"
)

var (
	ErrEnterpriseModelAliasInvalid         = errors.New("invalid enterprise model alias")
	ErrEnterpriseModelAliasConflict        = errors.New("enterprise model alias conflicts with an existing model")
	ErrEnterpriseModelAliasUnsupported     = errors.New("upstream model is not available")
	ErrEnterpriseModelAliasImmutable       = errors.New("enterprise model alias and upstream model cannot be changed after creation")
	ErrEnterpriseModelAliasForbidden       = errors.New("enterprise model alias is not owned by this enterprise")
	ErrEnterpriseModelAliasNotFound        = errors.New("enterprise model alias not found")
	ErrEnterpriseModelAliasVersionConflict = errors.New("enterprise model alias version conflict")
)

var (
	enterpriseModelAliasIDPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	enterpriseUpstreamModelIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+\-]{0,254}$`)
)

// EnterpriseModelAlias maps a WorkBuddy-facing identifier to a canonical
// new-api model for one enterprise owner. Rows are tombstoned instead of
// deleted so an old client cannot later fall through to a same-named model.
type EnterpriseModelAlias struct {
	ID              uint   `json:"id" gorm:"primaryKey"`
	OwnerUserID     int    `json:"owner_user_id" gorm:"not null;uniqueIndex:idx_enterprise_alias_owner_source;uniqueIndex:idx_enterprise_alias_owner_alias"`
	SourceID        string `json:"source_id" gorm:"type:varchar(128);not null;uniqueIndex:idx_enterprise_alias_owner_source"`
	Alias           string `json:"alias" gorm:"type:varchar(128);not null;uniqueIndex:idx_enterprise_alias_owner_alias"`
	UpstreamModelID string `json:"upstream_model_id" gorm:"type:varchar(255);not null"`
	Status          int    `json:"status" gorm:"not null;default:1;index"`
	Version         uint64 `json:"version" gorm:"not null;default:1"`
	CreatedAt       int64  `json:"created_at" gorm:"autoCreateTime;column:created_at"`
	UpdatedAt       int64  `json:"updated_at" gorm:"autoUpdateTime;column:updated_at"`
}

func (EnterpriseModelAlias) TableName() string {
	return "enterprise_model_aliases"
}

type EnterpriseModelAliasMutation struct {
	SourceID        string  `json:"source_id"`
	Alias           string  `json:"alias"`
	UpstreamModelID string  `json:"upstream_model_id"`
	Enabled         *bool   `json:"enabled,omitempty"`
	ExpectedVersion *uint64 `json:"expected_version,omitempty"`
}

func UpsertEnterpriseModelAlias(ownerUserID int, mutation EnterpriseModelAliasMutation) (EnterpriseModelAlias, error) {
	mutation.SourceID = strings.TrimSpace(mutation.SourceID)
	mutation.Alias = strings.TrimSpace(mutation.Alias)
	mutation.UpstreamModelID = strings.TrimSpace(mutation.UpstreamModelID)
	if err := validateEnterpriseModelAliasMutation(ownerUserID, mutation); err != nil {
		return EnterpriseModelAlias{}, err
	}

	var result EnterpriseModelAlias
	desiredStatus := EnterpriseModelAliasStatusActive
	if mutation.Enabled != nil && !*mutation.Enabled {
		desiredStatus = EnterpriseModelAliasStatusDisabled
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current EnterpriseModelAlias
		err := lockForUpdate(tx).
			Where("owner_user_id = ? AND source_id = ?", ownerUserID, mutation.SourceID).
			First(&current).Error
		if err == nil {
			if current.Alias != mutation.Alias || current.UpstreamModelID != mutation.UpstreamModelID {
				return ErrEnterpriseModelAliasImmutable
			}
			if current.Status == EnterpriseModelAliasStatusTombstone {
				return ErrEnterpriseModelAliasConflict
			}
			if current.Status == desiredStatus {
				// An exact retry is idempotent even if the caller never received the
				// original version after a successful network request.
				result = current
				return nil
			}
			if mutation.ExpectedVersion == nil || *mutation.ExpectedVersion != current.Version {
				return ErrEnterpriseModelAliasVersionConflict
			}
			update := tx.Model(&EnterpriseModelAlias{}).
				Where("id = ? AND version = ? AND status <> ?", current.ID, current.Version, EnterpriseModelAliasStatusTombstone).
				Updates(map[string]any{
					"status":  desiredStatus,
					"version": current.Version + 1,
				})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrEnterpriseModelAliasVersionConflict
			}
			current.Status = desiredStatus
			current.Version++
			result = current
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if mutation.ExpectedVersion != nil && *mutation.ExpectedVersion != 0 {
			return ErrEnterpriseModelAliasVersionConflict
		}

		var aliasCount int64
		if err := tx.Model(&EnterpriseModelAlias{}).
			Where("owner_user_id = ? AND alias = ?", ownerUserID, mutation.Alias).
			Count(&aliasCount).Error; err != nil {
			return err
		}
		if aliasCount > 0 {
			return ErrEnterpriseModelAliasConflict
		}

		var chainedAliasCount int64
		if err := tx.Model(&EnterpriseModelAlias{}).
			Where("owner_user_id = ? AND alias = ?", ownerUserID, mutation.UpstreamModelID).
			Count(&chainedAliasCount).Error; err != nil {
			return err
		}
		if chainedAliasCount > 0 {
			return ErrEnterpriseModelAliasUnsupported
		}

		var upstreamAbilityCount int64
		if err := tx.Model(&Ability{}).
			Where("model = ? AND enabled = ?", mutation.UpstreamModelID, true).
			Count(&upstreamAbilityCount).Error; err != nil {
			return err
		}
		if upstreamAbilityCount == 0 {
			return ErrEnterpriseModelAliasUnsupported
		}

		// An alias may never shadow a canonical model, including one whose
		// channel is currently disabled and could later be re-enabled.
		var canonicalCollisionCount int64
		if err := tx.Model(&Ability{}).
			Where("model = ?", mutation.Alias).
			Count(&canonicalCollisionCount).Error; err != nil {
			return err
		}
		if canonicalCollisionCount > 0 {
			return ErrEnterpriseModelAliasConflict
		}

		result = EnterpriseModelAlias{
			OwnerUserID:     ownerUserID,
			SourceID:        mutation.SourceID,
			Alias:           mutation.Alias,
			UpstreamModelID: mutation.UpstreamModelID,
			Status:          desiredStatus,
			Version:         1,
		}
		if err := tx.Create(&result).Error; err != nil {
			if isEnterpriseModelAliasUniqueError(err) {
				return ErrEnterpriseModelAliasConflict
			}
			return err
		}
		return nil
	})
	return result, err
}

func TombstoneEnterpriseModelAlias(ownerUserID int, sourceID string, expectedVersion *uint64) (EnterpriseModelAlias, error) {
	sourceID = strings.TrimSpace(sourceID)
	if ownerUserID <= 0 || !enterpriseModelAliasIDPattern.MatchString(sourceID) {
		return EnterpriseModelAlias{}, ErrEnterpriseModelAliasInvalid
	}

	var result EnterpriseModelAlias
	err := DB.Transaction(func(tx *gorm.DB) error {
		err := lockForUpdate(tx).
			Where("owner_user_id = ? AND source_id = ?", ownerUserID, sourceID).
			First(&result).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var otherOwnerCount int64
			if countErr := tx.Model(&EnterpriseModelAlias{}).
				Where("source_id = ? AND owner_user_id <> ?", sourceID, ownerUserID).
				Count(&otherOwnerCount).Error; countErr != nil {
				return countErr
			}
			if otherOwnerCount > 0 {
				return ErrEnterpriseModelAliasForbidden
			}
			return ErrEnterpriseModelAliasNotFound
		}
		if err != nil {
			return err
		}
		if result.Status == EnterpriseModelAliasStatusTombstone {
			return nil
		}
		if expectedVersion == nil || *expectedVersion != result.Version {
			return ErrEnterpriseModelAliasVersionConflict
		}

		currentVersion := result.Version
		update := tx.Model(&EnterpriseModelAlias{}).
			Where("id = ? AND version = ? AND status <> ?", result.ID, currentVersion, EnterpriseModelAliasStatusTombstone).
			Updates(map[string]any{
				"status":  EnterpriseModelAliasStatusTombstone,
				"version": currentVersion + 1,
			})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrEnterpriseModelAliasVersionConflict
		}
		result.Status = EnterpriseModelAliasStatusTombstone
		result.Version = currentVersion + 1
		return nil
	})
	return result, err
}

func ResolveEnterpriseModelAlias(ownerUserID int, alias string) (EnterpriseModelAlias, bool, error) {
	alias = strings.TrimSpace(alias)
	if ownerUserID <= 0 || alias == "" {
		return EnterpriseModelAlias{}, false, nil
	}
	var result EnterpriseModelAlias
	err := DB.Where("owner_user_id = ? AND alias = ?", ownerUserID, alias).First(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return EnterpriseModelAlias{}, false, nil
	}
	if err != nil {
		return EnterpriseModelAlias{}, false, err
	}
	return result, true, nil
}

func GetEnterpriseModelAliasBySource(ownerUserID int, sourceID string) (EnterpriseModelAlias, error) {
	sourceID = strings.TrimSpace(sourceID)
	if ownerUserID <= 0 || !enterpriseModelAliasIDPattern.MatchString(sourceID) {
		return EnterpriseModelAlias{}, ErrEnterpriseModelAliasInvalid
	}
	var result EnterpriseModelAlias
	err := DB.Where("owner_user_id = ? AND source_id = ?", ownerUserID, sourceID).First(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return EnterpriseModelAlias{}, ErrEnterpriseModelAliasNotFound
	}
	return result, err
}

func validateEnterpriseModelAliasMutation(ownerUserID int, mutation EnterpriseModelAliasMutation) error {
	if ownerUserID <= 0 ||
		!enterpriseModelAliasIDPattern.MatchString(mutation.SourceID) ||
		!enterpriseModelAliasIDPattern.MatchString(mutation.Alias) ||
		!enterpriseUpstreamModelIDPattern.MatchString(mutation.UpstreamModelID) ||
		strings.HasPrefix(strings.ToLower(mutation.Alias), "custom-local:") ||
		strings.HasSuffix(strings.ToLower(mutation.Alias), enterpriseReservedCompactSuffix) ||
		mutation.Alias == mutation.UpstreamModelID {
		return ErrEnterpriseModelAliasInvalid
	}
	return nil
}

func isEnterpriseModelAliasUniqueError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}
