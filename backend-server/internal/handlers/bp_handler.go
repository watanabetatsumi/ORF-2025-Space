package handlers

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/application/model"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/application/service"
	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/middleware"
)

type bpHandler struct {
	bpService  *service.BpService
	middleware *middleware.MiddlewarePlugins
}

func NewBpHandler(bpService *service.BpService, middlware *middleware.MiddlewarePlugins) *bpHandler {
	return &bpHandler{
		bpService:  bpService,
		middleware: middlware,
	}
}

func (bh *bpHandler) GetContent(c *gin.Context) {
	r := c.Request
	w := c.Writer

	// デバッグログ: リクエストの詳細を出力
	log.Printf("[BpHandler] Received request: Method=%s, Path=%s, Query=%s, Host=%s",
		r.Method, r.URL.Path, r.URL.RawQuery, r.Host)

	// CONNECTメソッドの場合は特別な処理が必要
	if r.Method == http.MethodConnect {
		log.Printf("[BpHandler] Processing CONNECT method")
		bh.handleCONNECT(c)
		return
	}

	// 転送されてくるHTTPリクエストを処理（GET、POST、PUT、DELETE、PATCHなどすべてのメソッドに対応）
	// 転送先URLをクエリパラメータから取得
	targetURL := r.URL.Query().Get("url")
	if targetURL == "" {
		http.Error(w, "url parameter is required", http.StatusBadRequest)
		return
	}

	// URLの検証
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}

	// リクエストボディを読み込む
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			return
		}
		r.Body.Close()
	}

	breq := model.BpRequest{
		Method:        r.Method,
		URL:           parsedURL.String(),
		Headers:       r.Header,
		Body:          bodyBytes,
		ContentType:   r.Header.Get("Content-Type"),
		ContentLength: r.ContentLength,
	}

	log.Printf("[BpHandler] Received request: Method=%s, URL=%s", breq.Method, breq.URL)

	// Service層でリクエストを転送（キャッシュ可能な場合はキャッシュもチェック）
	// リクエストのcontextを取得して伝播（キャンセレーションやタイムアウト制御のため）
	ctx := r.Context()
	resp, err := bh.bpService.ProxyRequest(ctx, &breq)
	if err != nil {
		http.Error(w, "Failed to proxy request", http.StatusBadGateway)
		return
	}

	// レスポンスヘッダーをコピー
	for key, values := range resp.Headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// ステータスコードを設定
	w.WriteHeader(resp.StatusCode)

	// レスポンスボディをコピー
	_, err = io.Copy(w, resp.GetBodyReader())
	if err != nil {
		http.Error(w, "Failed to copy response body", http.StatusInternalServerError)
		return
	}
}

// handleCONNECT CONNECTメソッドのリクエストを処理（HTTPトンネリング）
func (bh *bpHandler) handleCONNECT(c *gin.Context) {
	w := c.Writer

	// Hijackして双方向のストリーム転送を開始（socket?）
	// 注意: Hijackする前にヘッダーを書き込んではいけない
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	// ============================================
	// 通信に割り込んで除き見る
	// ============================================

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "Failed to hijack connection", http.StatusInternalServerError)
		return
	}
	// 注意: ここでdefer clientConn.Close()をすると、HandleConnection内で閉じられる前に接続が切れる可能性があるため、
	// ハンドリングをミドルウェアに任せるか、エラー時のみ閉じるようにする。
	// 今回はSSLBumpHandler.HandleConnectionが成功した場合、そちらで管理されるため、
	// エラー時または処理完了後に適切に閉じる必要がある。
	// ひとまずここでは、ミドルウェア呼び出し後に閉じるようにする。
	defer clientConn.Close()

	// ============================================
	// 接続を確立させる(終端)
	// ============================================

	// CONNECTメソッドのレスポンスを返す（200 Connection Established）
	// クライアントに対してトンネル確立を通知
	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// ============================================
	// TLSハンドシェイクとリクエストの復号化
	// ============================================

	// SSLBumpHandlerを使って中間者攻撃（MitM）を開始
	// クライアントとのTLSハンドシェイクを行う
	tlsConn, err := bh.middleware.SSLBumpHandler.HandleConnection(clientConn)
	if err != nil {
		log.Printf("[BpHandler] SSL Bump failed: %v", err)
		return
	}
	defer tlsConn.Close() // TLS接続を閉じる

	// ============================================
	// 復号化されたリクエストの処理
	// ============================================

	// TLS接続からHTTPリクエストを読み込む
	// 注意: http.ReadRequestはbufio.Readerを要求するためラップする
	bufReader := bufio.NewReader(tlsConn)
	req, err := http.ReadRequest(bufReader)
	if err != nil {
		log.Printf("[BpHandler] Failed to read HTTP request from TLS connection: %v", err)
		return
	}

	// リクエストボディを読み込む
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			log.Printf("[BpHandler] Failed to read request body: %v", err)
			return
		}
		req.Body.Close()
	}

	// BpRequestを作成
	bpReq := &model.BpRequest{
		Method:        req.Method,
		URL:           req.URL.String(),
		Headers:       req.Header,
		Body:          bodyBytes,
		ContentType:   req.Header.Get("Content-Type"),
		ContentLength: req.ContentLength,
	}

	// スキームが欠落している場合（サーバーリクエストで一般的）、完全なURLを再構築する
	if req.URL.Scheme == "" {
		scheme := "https"
		host := req.Host
		if host == "" {
			host = "unknown"
		}
		bpReq.URL = fmt.Sprintf("%s://%s%s", scheme, host, req.URL.Path)
		if req.URL.RawQuery != "" {
			bpReq.URL += "?" + req.URL.RawQuery
		}
	}

	log.Printf("[BpHandler] Decrypted request: Method=%s, URL=%s", bpReq.Method, bpReq.URL)

	// 取得したリクエストをService層で転送
	// contextは元のリクエストのものを使用できないため（Hijack済み）、新しいcontextを作成
	ctx := context.Background()
	resp, err := bh.bpService.ProxyRequest(ctx, bpReq)
	if err != nil {
		log.Printf("[BpHandler] Proxy request failed: %v", err)
		// エラーレスポンスをTLS接続に書き込む
		resp := &http.Response{
			StatusCode: http.StatusBadGateway,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Body:       io.NopCloser(strings.NewReader("Bad Gateway")),
		}
		resp.Write(tlsConn)
		return
	}
	defer resp.GetBodyReader().(io.ReadCloser).Close()

	// レスポンスをクライアント（TLS接続）に書き込む
	// http.Responseを構築してWriteメソッドで書き込む
	httpResp := &http.Response{
		StatusCode:    resp.StatusCode,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        make(http.Header),
		Body:          io.NopCloser(resp.GetBodyReader()),
		ContentLength: resp.ContentLength,
	}
	// ヘッダーをコピー
	for key, values := range resp.Headers {
		for _, value := range values {
			httpResp.Header.Add(key, value)
		}
	}

	// レスポンスを書き込む
	if err := httpResp.Write(tlsConn); err != nil {
		log.Printf("[BpHandler] Failed to write response: %v", err)
	}
}
