package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/application/model"
)

type BpGateway struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	client *http.Client
}

func NewBpGateway(host string, port int, timeout time.Duration) *BpGateway {
	return &BpGateway{
		Host: host,
		Port: port,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (g *BpGateway) ProxyRequest(ctx context.Context, breq *model.BpRequest) (*model.BpResponse, error) {
	// breq.URLは既にhandler層で検証済みなので、ParseURL()はエラーにならない
	parsedURL, _ := breq.ParseURL()

	// 転送先URLを構築
	targetURL := _buildTargetURL(g.Host, g.Port, parsedURL)

	// HTTPリクエストを作成（contextを設定）
	httpReq, err := http.NewRequestWithContext(ctx, breq.Method, targetURL, bytes.NewReader(breq.Body))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	breq.SetHeaders(httpReq)

	// HTTPリクエストを送信
	httpResp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to forward HTTP request: %w", err)
	}
	defer httpResp.Body.Close()

	// レスポンスボディを読み込む
	bodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// BpResponseを作成
	return &model.BpResponse{
		StatusCode:    httpResp.StatusCode,
		Headers:       httpResp.Header,
		Body:          bodyBytes,
		ContentType:   httpResp.Header.Get("Content-Type"),
		ContentLength: httpResp.ContentLength,
	}, nil
}

// buildTargetURL 指定されたホストとポート、パース済みURLから転送先URLを構築する
func _buildTargetURL(host string, port int, parsedURL *url.URL) string {
	targetURL := fmt.Sprintf("http://%s:%d%s", host, port, parsedURL.Path)
	if parsedURL.RawQuery != "" {
		targetURL += "?" + parsedURL.RawQuery
	}
	return targetURL
}
