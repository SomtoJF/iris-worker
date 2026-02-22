package sqldb

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Resume struct {
	IdResume   uint      `gorm:"primaryKey;autoIncrement;column:id_resume" json:"_"`
	IdExternal uuid.UUID `gorm:"type:text;not null;unique" json:"id"`
	// Either url of filepath
	UserId       uint       `gorm:"column:id_user;not null"`
	User         User       `gorm:"foreignKey:UserId;references:IdUser"`
	FileKey      string     `gorm:"not null"`
	FileName     string     `gorm:"not null"`
	FileSize     int64      `gorm:"not null"`
	Content      string     `gorm:"not null"`
	IsProcessing bool       `gorm:"default:true"`
	IsActive     bool       `gorm:"default:true"`
	CreatedAt    time.Time  `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt    time.Time  `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt    *time.Time `gorm:"index;default:NULL"`
}

func (Resume) TableName() string {
	return "resume"
}

func (a *Activity) FetchActiveUserResume(ctx context.Context, idUser uint) (Resume, error) {
	var resume Resume
	if err := a.db.Model(&Resume{}).Where("id_user = ? AND is_active = ?", idUser, true).First(&resume).Error; err != nil {
		return Resume{}, err
	}
	return resume, nil
}
