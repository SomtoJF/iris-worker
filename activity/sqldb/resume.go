package sqldb

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Resume struct {
	IdResume   uint       `gorm:"primaryKey;autoIncrement;column:id_resume" json:"_"`
	IdExternal uuid.UUID  `gorm:"type:text;not null;unique" json:"id"`
	Path       string     `gorm:"not null"`
	Content    string     `gorm:"not null"`
	Summary    string     `gorm:"not null"`
	CreatedAt  time.Time  `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt  time.Time  `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt  *time.Time `gorm:"index;default:NULL"`
}

func (Resume) TableName() string {
	return "resume"
}

func (a *Activity) FetchActiveUserResume(ctx context.Context) (Resume, error) {
	var resume Resume
	if err := a.db.Model(&Resume{}).Where("is_active = ?", true).First(&resume).Error; err != nil {
		return Resume{}, err
	}
	return resume, nil
}
