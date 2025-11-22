package service

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/application/interface/gateway"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/application/interface/repository"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/application/model"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/utils"
)

type BpService struct {
	bpgateway       gateway.BpGateway
	bprepository    repository.BpRepository
	defaultDir      string
	defaultFileName string
}

func NewBpService(
	bpgateway gateway.BpGateway,
	bprepository repository.BpRepository,
	defaultDir string,
	defaultFileName string,
) *BpService {
	return &BpService{
		bpgateway:       bpgateway,
		bprepository:    bprepository,
		defaultDir:      defaultDir,
		defaultFileName: defaultFileName,
	}
}

// ProxyRequest HTTPリクエストを転送する（キャッシュ可能な場合はキャッシュもチェック）
func (bs *BpService) ProxyRequest(ctx context.Context, breq *model.BpRequest) (*model.BpResponse, error) {
	// キャッシュ不可の場合は直接転送
	if !breq.IsCacheable() {
		log.Printf("[BpService] リクエストはキャッシュ不可: Method=%s, URL=%s", breq.Method, breq.URL)
		return bs.bpgateway.ProxyRequest(ctx, breq)
	}

	log.Printf("[BpService] リクエストはキャッシュ可能: URL=%s", breq.URL)

	// キャッシュ可能な場合はキャッシュから取得
	cacheKey := breq.GenerateCacheKey()
	cachedResp, found, err := bs.bprepository.GetResponse(ctx, cacheKey)
	// found == false の場合はキャッシュミス（エラーではない）
	if err != nil {
		log.Printf("[BpService] キャッシュ取得エラー: %v", err)
		// キャッシュ取得エラー: Gateway層で直接転送
		return bs.bpgateway.ProxyRequest(ctx, breq)
	}

	if found {
		log.Printf("[BpService] キャッシュヒット: URL=%s", breq.URL)
		// キャッシュヒット: キャッシュされたレスポンスを返す
		return cachedResp, nil
	}

	log.Printf("[BpService] キャッシュミス: URL=%s, リクエストを予約します", breq.URL)

	// リクエストの種類に応じたプレースホルダーを取得
	placeholderBody, contentType, err := utils.GetPlaceholderContent(breq.URL, bs.defaultDir)

	// 画像の場合は予約しない
	isImage := strings.HasPrefix(contentType, "image/")
	if isImage {
		log.Printf("[BpService] 画像リクエストのため予約をスキップします: URL=%s", breq.URL)
	} else {
		// キャッシュミス: Worker Poolにリクエストを予約してデフォルトページを返す
		if bs.bprepository != nil {
			err := bs.bprepository.ReserveRequest(ctx, breq)
			if err != nil {
				log.Printf("[BpService] ReserveRequest エラー: %v", err)
			} else {
				log.Printf("[BpService] ReserveRequest 成功: URL=%s", breq.URL)
			}
		}
	}

	if err == nil && placeholderBody != nil {
		return &model.BpResponse{
			StatusCode:    200,
			Headers:       make(map[string][]string),
			Body:          placeholderBody,
			ContentType:   contentType,
			ContentLength: int64(len(placeholderBody)),
		}, nil
	}

	// プレースホルダーが生成されなかった場合（HTMLなど）はデフォルトページを読み込む
	defaultPagePath := filepath.Join(bs.defaultDir, bs.defaultFileName)
	htmlBytes, err := utils.LoadDefaultPage(defaultPagePath)
	if err != nil {
		// デフォルトページの読み込みに失敗した場合は503 Service Unavailableを返す
		// DTN環境では直接転送は期待できないため、フォールバックとしてエラーを返す
		log.Printf("[BpService] Failed to load default page: %v", err)
		body := []byte("503 Service Unavailable: Failed to load default page and direct proxy is unavailable in DTN environment.")
		return &model.BpResponse{
			StatusCode:    http.StatusServiceUnavailable,
			Headers:       make(map[string][]string),
			Body:          body,
			ContentType:   "text/plain; charset=utf-8",
			ContentLength: int64(len(body)),
		}, nil
	}

	return &model.BpResponse{
		StatusCode:    200,
		Headers:       breq.Headers,
		Body:          htmlBytes,
		ContentType:   "text/html; charset=utf-8",
		ContentLength: int64(len(htmlBytes)),
	}, nil
}
