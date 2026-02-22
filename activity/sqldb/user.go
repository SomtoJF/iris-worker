package sqldb

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	IdUser       uint       `gorm:"primaryKey;autoIncrement;column:id_user" json:"_"`
	IdExternal   uuid.UUID  `gorm:"type:text;not null;unique" json:"id"`
	FirstName    string     `gorm:"not null"`
	LastName     string     `gorm:"not null"`
	Email        string     `gorm:"uniqueIndex;not null"`
	PasswordHash string     `gorm:"not null"`
	CreatedAt    time.Time  `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt    time.Time  `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt    *time.Time `gorm:"index;default:NULL"`
}

func (User) TableName() string {
	return "user"
}
