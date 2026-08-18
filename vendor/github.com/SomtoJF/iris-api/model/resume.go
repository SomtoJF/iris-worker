package model

import (
	"time"

	"github.com/google/uuid"
)

type Resume struct {
	IdResume   uint      `gorm:"primaryKey;autoIncrement;column:id_resume" json:"_"`
	IdExternal uuid.UUID `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	// Either url of filepath
	UserId       uint       `gorm:"column:id_user;not null"`
	User         User       `gorm:"foreignKey:UserId;references:IdUser"`
	DisplayName  *string    `gorm:"default:NULL" json:"displayName,omitempty"`
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
