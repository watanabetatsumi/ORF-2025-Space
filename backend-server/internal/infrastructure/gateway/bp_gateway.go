package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/application/model"
)

// 受信JSONデータの構造定義
type DTNJsonResponse struct {
	StatusCode    int                 `json:"status_code"`
	Headers       map[string][]string `json:"headers"`
	Body          string              `json:"body"` // Base64文字列
	ContentType   string              `json:"content_type"`
	ContentLength int64               `json:"content_length"`
}

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

	// ヘッダーの設定
	// (breq.SetHeadersが存在しない場合のフォールバック実装)
	if breq.Headers != nil {
		for key, values := range breq.Headers {
			for _, v := range values {
				httpReq.Header.Add(key, v)
			}
		}
	}

	// ---------------------------------------------------------
	// 変更点: HTTPクライアント(g.client.Do)ではなく、
	// 独自の sendrequest 関数を使って DTN (bpsendfile/bprecvfile) 経由で通信する
	// ---------------------------------------------------------
	httpResp, err := sendrequest(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to forward HTTP request via DTN: %w", err)
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

// sendrequest: HTTPリクエストをBundleとして送信し、レスポンスBundleを受信して返す
func sendrequest(req *http.Request) (*http.Response, error) {
	// ==========================================
	// 1. 送信処理 (以前の bpsendfile 実装)
	// ==========================================
	requestDir := "./request"

	// ディレクトリ作成
	if _, err := os.Stat(requestDir); os.IsNotExist(err) {
		if err := os.Mkdir(requestDir, 0755); err != nil {
			return nil, fmt.Errorf("error creating directory: %v", err)
		}
	}

	// ユニークなファイル名を作成
	filename := fmt.Sprintf("req_%d.txt", time.Now().UnixNano())
	filePath := filepath.Join(requestDir, filename)

	// HTTPリクエストのBodyを読み出してファイルに書き込む
	// (テキスト形式のリクエスト全体が必要な場合はDumpRequest等を使いますが、
	//  現状の実装に合わせてBodyを書き出します)
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		// 読み取ったBodyを後続処理のために戻しておく
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	if err := os.WriteFile(filePath, bodyBytes, 0644); err != nil {
		return nil, fmt.Errorf("error writing to file: %v", err)
	}
	fmt.Printf("Created request file: %s\n", filePath)

	// bpsendfile コマンド実行
	// 送信元: ipn:149.1 -> 宛先: ipn:150.1
	cmdSend := exec.Command("bpsendfile", "ipn:149.1", "ipn:150.1", filePath)
	output, err := cmdSend.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("error executing bpsendfile: %v, output: %s", err, string(output))
	}
	fmt.Printf("bpsendfile output: %s\n", string(output))

	// ==========================================
	// 2. 受信処理 (以前の bprecvfile 実装)
	// ==========================================
	recvEID := "ipn:149.2"
	targetFile := "testfile1" // IONの仕様でファイル名が決まるため固定

	// 前回の受信ファイルが残っていたら削除
	if _, err := os.Stat(targetFile); err == nil {
		_ = os.Remove(targetFile)
	}

	fmt.Printf("Waiting for response bundle at %s...\n", recvEID)

	// bprecvfile を実行（受信完了までブロック）
	cmdRecv := exec.Command("bprecvfile", recvEID, "1")
	if err := cmdRecv.Run(); err != nil {
		return nil, fmt.Errorf("error executing bprecvfile: %v", err)
	}

	// 受信ファイルの確認
	if _, err := os.Stat(targetFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("received file %s not found", targetFile)
	}

	// ファイル読み込み
	fileContent, err := ioutil.ReadFile(targetFile)
	if err != nil {
		return nil, fmt.Errorf("error reading received file: %v", err)
	}

	// 処理後は即削除 (次回のために)
	defer os.Remove(targetFile)

	// ==========================================
	// 3. レスポンス解析 (JSON -> http.Response)
	// ==========================================
	
	var dtnResp DTNJsonResponse
	if err := json.Unmarshal(fileContent, &dtnResp); err != nil {
		return nil, fmt.Errorf("error parsing received JSON: %v", err)
	}

	// Body (Base64) のデコード
	decodedBodyBytes, err := base64.StdEncoding.DecodeString(dtnResp.Body)
	if err != nil {
		return nil, fmt.Errorf("error decoding base64 body: %v", err)
	}

	// ヘッダーの構築
	httpHeader := make(http.Header)
	for k, v := range dtnResp.Headers {
		for _, hVal := range v {
			httpHeader.Add(k, hVal)
		}
	}

	// http.Response を作成して返す
	return &http.Response{
		StatusCode:    dtnResp.StatusCode,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        httpHeader,
		Body:          io.NopCloser(bytes.NewReader(decodedBodyBytes)),
		ContentLength: dtnResp.ContentLength,
		Request:       req,
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