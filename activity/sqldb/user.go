package sqldb

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type User struct {
	IdUser                uint                  `gorm:"primaryKey;autoIncrement;column:id_user" json:"_"`
	IdExternal            uuid.UUID             `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	JobApplicationProfile JobApplicationProfile `gorm:"foreignKey:UserId;references:IdUser"`
	FirstName             string                `gorm:"not null"`
	LastName              string                `gorm:"not null"`
	Email                 string                `gorm:"uniqueIndex;not null"`
	PasswordHash          string                `gorm:"not null"`
	IsAdmin               bool                  `gorm:"default:false"`
	CreatedAt             time.Time             `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt             time.Time             `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt             *time.Time            `gorm:"index;default:NULL"`
}

func (User) TableName() string {
	return "user"
}

type GetUserInput struct {
	IdUser                       uint `json:"id_user"`
	IncludeJobApplicationProfile bool `json:"include_job_application_profile"`
}

func (a *Activity) GetUser(ctx context.Context, input GetUserInput) (User, error) {
	var user User
	db := a.db.Model(&User{})
	if input.IncludeJobApplicationProfile {
		db = db.Preload("JobApplicationProfile")
	}
	if err := db.Where("id_user = ?", input.IdUser).First(&user).Error; err != nil {
		return User{}, err
	}
	return user, nil
}
