package worker

import (
	"context"
	"log"
	"time"

	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/application/interface/gateway"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/application/interface/repository"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/application/model"
)

type RequestHandler struct {
	bprepo     repository.BpRepository
	bpgateway  gateway.BpGateway
	defaultTTL time.Duration
}

func NewRequestHandler(
	bprepo repository.BpRepository,
	bpgateway gateway.BpGateway,
	defaultTTL time.Duration,
) *RequestHandler {
	return &RequestHandler{
		bprepo:     bprepo,
		bpgateway:  bpgateway,
		defaultTTL: defaultTTL,
	}
}

// HandleRequest 予約されたリクエストを処理してキャッシュに保存
func (rh *RequestHandler) HandleRequest(ctx context.Context, req *model.BpRequest, workerID int) error {
	log.Printf("[Worker %d] リクエスト処理開始: %s", workerID, req.URL)

	// // レスポンスのキャッシュが既に存在しないかをチェックする
	cacheKey := req.GenerateCacheKey()
	_, found, err := rh.bprepo.GetResponse(ctx, cacheKey)
	if err != nil {
		log.Printf("[Worker %d] キャッシュ確認中にエラーが発生しました (URL: %s): %v", workerID, req.URL, err)
		// エラーがあっても実行を継続する
	} else if found {
		log.Printf("[Worker %d] 既にキャッシュが存在するため処理をスキップします (URL: %s)", workerID, req.URL)
		// 予約は削除する
		_ = rh._removeReservedRequest(ctx, req, workerID)
		return nil
	}

	// Gatewayでリクエストを転送
	resp, err := rh.bpgateway.ProxyRequest(ctx, req)
	if err != nil {
		log.Printf("[Worker %d] リクエストの転送に失敗 (URL: %s): %v", workerID, req.URL, err)

		// エラーが発生しても予約は削除（次回再試行）
		// return rh._removeReservedRequest(ctx, req, workerID)
		return nil
	}

	// 追加: ステータスコードが200以外（特にリダイレクトやエラー）はキャッシュしない
	if resp.StatusCode != 200 {
		log.Printf("[Worker %d] ステータスコードが200ではないためキャッシュしません (URL: %s, Status: %d)", workerID, req.URL, resp.StatusCode)
		// 予約だけ削除して終了
		_ = rh._removeReservedRequest(ctx, req, workerID)
		return nil
	}

	// レスポンスをキャッシュに保存（URLベースの階層構造で保存）
	cache_ttl := rh.defaultTTL // 設定値を使用

	// SetResponseWithURLを使用してURLベースの階層構造でキャッシュを保存
	err = rh.bprepo.SetResponseWithURL(ctx, req, resp, cache_ttl)
	if err != nil {
		log.Printf("[Worker %d] キャッシュの保存に失敗 (URL: %s): %v", workerID, req.URL, err)

		// キャッシュ保存に失敗しても予約は削除
		// return rh._removeReservedRequest(ctx, req, workerID)
	}

	// 予約を削除
	err = rh._removeReservedRequest(ctx, req, workerID)
	if err != nil {
		return err
	}

	log.Printf("[Worker %d] リクエスト処理完了: %s", workerID, req.URL)

	return nil
}

func (rh *RequestHandler) _removeReservedRequest(ctx context.Context, req *model.BpRequest, workerID int) error {
	err := rh.bprepo.RemoveReservedRequest(ctx, req)
	if err != nil {
		log.Printf("[Worker %d] 予約の削除に失敗 (URL: %s): %v", workerID, req.URL, err)
	}

	log.Printf("[Worker %d] リクエストは削除されました (URL: %s)", workerID, req.URL)

	return err
}
