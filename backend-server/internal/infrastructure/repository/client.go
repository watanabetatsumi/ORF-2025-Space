package repository

import (
	"context"
	"time"
)

type CacheItem struct {
	Key      string
	FilePath string
}

type BpRepoClient interface {
	GetMetaData(ctx context.Context, key string) ([]byte, error)
	ScanExpiredCacheKeys(ctx context.Context) ([]CacheItem, error)
	SetMetaData(ctx context.Context, key string, data []byte, ttl time.Duration) error
	DeleteCache(ctx context.Context, metaKey string, filePath string) error
	ReserveRequest(ctx context.Context, job []byte) error
	GetReservedRequests(ctx context.Context) ([][]byte, error)
	RemoveReservedRequest(ctx context.Context, job []byte) error
	BLPopReservedRequest(ctx context.Context, timeout time.Duration) ([]byte, error)
	FlushAll(ctx context.Context) error
}
