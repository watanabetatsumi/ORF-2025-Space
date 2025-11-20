package main

import (
	"context"
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/cmd/config"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/application/service"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/handlers"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/infrastructure/gateway"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/infrastructure/repository"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/infrastructure/repository/plugins"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/middleware"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/middleware/module"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/scheduler"
	scheduler_worker "github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/scheduler/worker"
)

func main() {
	// ============================================
	// 設定とインフラストラクチャの初期化
	// ============================================
	conf := config.LoadConfig()

	// Redisクライアントの初期化
	redisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", conf.RedisClient.Host, conf.RedisClient.Port),
		Password: conf.RedisClient.Password,
		DB:       conf.RedisClient.DB,
	})
	repoClient := plugins.NewRedisClient(redisClient)

	// 依存関係の初期化
	bpgw := gateway.NewBpGateway(conf.BPGateway.Host, conf.BPGateway.Port, conf.BPGateway.Timeout)
	// bpgw := gateway.NewLocalGateway(conf.BPGateway.Timeout) // ローカルGatewayを使用
	bprepo := repository.NewBpRepository(repoClient, conf.Cache.Dir)

	// ============================================
	// ミドルウェアの初期化
	// ============================================

	ssl_bump_app, err := module.NewSSLBumpHandler(conf.Middlware.CertPath, conf.Middlware.KeyPath, conf.Middlware.MaxCacheSize)
	if err != nil {
		log.Fatalf("Failed to initialize SSLBumpHandler: %v", err)
		return
	}
	middlwares := middleware.NewMiddlewarePlugins(
		ssl_bump_app,
	)

	// ============================================
	// アプリケーション層の初期化
	// ============================================

	bpsrv := service.NewBpService(bpgw, bprepo)
	bpHandler := handlers.NewBpHandler(bpsrv, middlwares)

	// ============================================
	// HTTPサーバーの設定
	// ============================================

	r := gin.Default()

	// 管理用エンドポイント: 期限切れキャッシュの一括削除
	r.POST("/system/admin/cache/cleanup", func(c *gin.Context) {
		ctx := c.Request.Context()
		err := bprepo.DeleteExpiredCaches(ctx)
		if err != nil {
			c.JSON(500, gin.H{
				"error":   "Failed to cleanup expired cache",
				"message": err.Error(),
			})
			return
		}
		c.JSON(200, gin.H{
			"message": "Expired cache cleanup completed successfully",
		})
	})

	// CONNECTメソッドを処理するミドルウェアを追加
	// CONNECTメソッドのリクエストは、パスがhost:port形式になる可能性があるため、
	// NoRouteの前に処理する必要がある
	r.Use(func(c *gin.Context) {
		if c.Request.Method == "CONNECT" {
			bpHandler.GetContent(c)
			c.Abort()
			return
		}
		c.Next()
	})

	// すべてのHTTPメソッド（GET、POST、PUT、DELETE、PATCHなど）とパスに対応
	// NoRouteは既存のルートにマッチしないすべてのリクエストを処理する
	r.NoRoute(bpHandler.GetContent)

	// ============================================
	// Worker Poolの起動（非同期リクエスト処理）
	// ============================================
	// プラグイン可能なWorker実装を使用
	reqHandler := scheduler_worker.NewRequestHandler(bprepo, bpgw, conf.Cache.DefaultTTL)
	queueWatcher := scheduler_worker.NewQueueWatcher(bprepo, conf.Worker.QueueWatchTimeout)
	cacheHandler := scheduler_worker.NewCacheHandler(bprepo)
	processor := scheduler.NewRequestProcessor(conf.Worker.Workers, reqHandler, queueWatcher, cacheHandler, conf.Cache.CleanupInterval) // 5つのworker
	ctx := context.Background()
	processor.Start(ctx)

	// ============================================
	// HTTPサーバーの起動
	// ============================================
	log.Println("HTTPサーバーを起動します... (ポート: 8082)")
	if err := r.Run(":8082"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
