package config

import "time"

type BpGateway struct {
	Host    string
	Port    int
	Timeout time.Duration // HTTPクライアントのタイムアウト
}

type Redis struct {
	Host     string
	Port     int
	Password string
	DB       int
}

type CacheConfig struct {
	Dir             string        // キャッシュファイルを保存するディレクトリ
	DefaultTTL      time.Duration // デフォルトのキャッシュTTL
	CleanupInterval time.Duration // キャッシュクリーンアップの実行間隔
}

type WorkerConfig struct {
	Workers           int           // Worker Poolのワーカー数
	QueueWatchTimeout time.Duration // キュー監視のタイムアウト
}

type MiddlewareConfig struct {
	CertPath      string // ルート証明書のパス
	KeyPath       string // ルート秘密鍵のパス
	MaxCacheSize  int    // 証明書キャッシュの最大数
	RSABits       int    // RSA鍵のビット長
	CacheDuration int    // 生成した証明書の有効期間(時間)
}

type Config struct {
	BPGateway   BpGateway
	RedisClient Redis
	Cache       CacheConfig
	Worker      WorkerConfig
	Middlware   MiddlewareConfig
}

func LoadConfig() Config {
	return Config{
		BPGateway: BpGateway{
			Host:    "localhost",
			Port:    8081,
			Timeout: 180 * time.Second,
		},
		RedisClient: Redis{
			Host:     "localhost",
			Port:     6379,
			Password: "",
			DB:       0,
		},
		Cache: CacheConfig{
			Dir:             "./tmp/bp_cache",
			DefaultTTL:      24 * time.Hour,
			CleanupInterval: 5 * time.Minute,
		},
		Worker: WorkerConfig{
			Workers:           5,
			QueueWatchTimeout: 5 * time.Second,
		},
		Middlware: MiddlewareConfig{
			CertPath:      "./bump.crt",
			KeyPath:       "./bump.key",
			MaxCacheSize:  20,
			RSABits:       2048,
			CacheDuration: 24,
		},
	}
}
