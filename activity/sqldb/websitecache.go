package sqldb

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WebsiteCachePage struct {
	Title   string `json:"title"`
	Url     string `json:"url"`
	Content string `json:"content"`
}

type WebsiteCachePages []WebsiteCachePage

func (c *WebsiteCachePages) Scan(value any) error {
	if value == nil {
		*c = nil
		return nil
	}

	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("WebsiteCachePages.Scan: unsupported type %T", value)
	}

	if len(b) == 0 || string(b) == "null" {
		*c = nil
		return nil
	}

	var out WebsiteCachePages
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("WebsiteCachePages.Scan: %w", err)
	}
	*c = out
	return nil
}

func (c WebsiteCachePages) Value() (driver.Value, error) {
	if c == nil {
		return []byte("null"), nil
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("WebsiteCachePages.Value: %w", err)
	}
	return b, nil
}

type WebsiteCache struct {
	IdWebsiteCache uint              `gorm:"primaryKey;autoIncrement;column:id_website_cache" json:"_"`
	Domain         string            `gorm:"column:domain;not null;unique" json:"domain"`
	Pages          WebsiteCachePages `gorm:"type:jsonb;not null" json:"pages"`
	ExpiresAt      time.Time         `gorm:"column:expires_at;not null" json:"expires_at"`
	CreatedAt      time.Time         `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt      time.Time         `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP;autoUpdateTime" json:"updated_at"`
	DeletedAt      gorm.DeletedAt    `gorm:"column:deleted_at;default:NULL" json:"deleted_at"`
}

func (WebsiteCache) TableName() string {
	return "website_cache"
}

func (a *Activity) GetWebsiteCache(ctx context.Context, domain string) (WebsiteCache, error) {
	var cache WebsiteCache
	err := a.db.WithContext(ctx).
		Where("domain = ? AND expires_at > ? AND deleted_at IS NULL", domain, time.Now()).
		First(&cache).Error
	return cache, err
}

type SaveWebsiteCacheInput struct {
	Domain string            `json:"domain"`
	Pages  WebsiteCachePages `json:"pages"`
}

func (a *Activity) SaveWebsiteCache(ctx context.Context, input SaveWebsiteCacheInput) error {
	record := WebsiteCache{
		Domain:    input.Domain,
		Pages:     input.Pages,
		ExpiresAt: time.Now().AddDate(0, 6, 0),
	}
	return a.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "domain"}},
			DoUpdates: clause.AssignmentColumns([]string{"pages", "expires_at", "updated_at"}),
		}).
		Create(&record).Error
}
