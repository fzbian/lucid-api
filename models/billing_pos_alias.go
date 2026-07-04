package models

import "time"

// BillingPOSAlias vincula nombres antiguos de POS con el nombre actual.
type BillingPOSAlias struct {
	ID             uint       `json:"id" gorm:"primaryKey"`
	OldPosName     string     `json:"old_pos_name" gorm:"size:191;uniqueIndex"`
	CurrentPosName string     `json:"current_pos_name" gorm:"size:191;index"`
	Active         bool       `json:"active" gorm:"default:true;index"`
	LastSyncedAt   *time.Time `json:"last_synced_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (BillingPOSAlias) TableName() string {
	return "billing_pos_aliases"
}
