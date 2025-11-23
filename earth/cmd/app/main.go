package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"earth/bpsocket"
)

// DTNJsonRequest DTN経由で受信するリクエスト構造体
type DTNJsonRequest struct {
	RequestID string              `json:"request_id"`
	Method    string              `json:"method"`
	URL       string              `json:"url"`
	Headers   map[string][]string `json:"headers"`
	Body      string              `json:"body"` // Base64エンコード
	Version   int                 `json:"version"`
}

// CrawlRequest 内部処理用のクロールリクエスト構造体
type CrawlRequest struct {
	RequestID string
	URL       string
	Depth     int
}

// BpResponse HTTPレスポンスに必要な情報を格納する構造体
type BpResponse struct {
	RequestID     string              `json:"request_id"`
	StatusCode    int                 `json:"status_code"`
	Headers       map[string][]string `json:"headers"`
	Body          string              `json:"body"` // Base64エンコード
	ContentType   string              `json:"content_type,omitempty"`
	ContentLength int64               `json:"content_length,omitempty"`
	Depth         int                 `json:"-"` // 内部管理用 (JSONには含めない)
}

// 共通リソース
var (
	visitedURLs  = make(map[string]bool)
	visitedMutex sync.Mutex
	linkRegex    = regexp.MustCompile(`(?i)<a\s+(?:[^>]*?\s+)?href=["']?([^"'>\s]+)["']?`)
	maxDepth     = 2
)

func main() {
	log.Println("=== Earth Station with BP Socket Gateway ===")

	// BP Socket設定
	const (
		localNodeNum   = 150 // Earth node
		localSvcNum    = 1   // Receive on ipn:150.1
		sendFromSvcNum = 2   // Send from ipn:150.2
		remoteNodeNum  = 149 // Space node
		remoteSvcNum   = 1   // Send to ipn:149.1
	)

	// BP Socket Receiverの初期化
	receiver, err := bpsocket.NewBpReceiver(localNodeNum, localSvcNum)
	if err != nil {
		log.Fatalf("Failed to create BP receiver: %v", err)
	}
	defer receiver.Close()

	// BP Socket Senderの初期化
	sender, err := bpsocket.NewBpSender(localNodeNum, sendFromSvcNum, remoteNodeNum, remoteSvcNum)
	if err != nil {
		log.Fatalf("Failed to create BP sender: %v", err)
	}
	defer sender.Close()

	// パイプライン用チャネルの作成
	urlChan := make(chan CrawlRequest, 100)
	bpResChan := make(chan BpResponse, 100)
	sendChan := make(chan BpResponse, 100)

	var wg sync.WaitGroup

	// 受信ループを開始
	receiver.Start()

	// --- 1. Recv Stage (BP Socketから連続受信) ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		recvStageBpSocket(receiver.GetDataChannel(), urlChan)
	}()

	// --- 2. Fetch Stage (HTTPリクエスト実行) ---
	const fetchWorkers = 5
	for i := 0; i < fetchWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fetchWorkerBpSocket(urlChan, bpResChan)
		}()
	}

	// --- 3. Save & Recurse Stage (再帰処理とsendChanへの転送) ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		saveAndRecurseWorkerBpSocket(bpResChan, urlChan, sendChan)
	}()

	// --- 4. Send Stage (BP Socketで送信) ---
	const sendWorkers = 3
	for i := 0; i < sendWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sendWorkerBpSocket(sendChan, sender, workerID)
		}(i)
	}

	log.Println("Earth Station is running with BP Socket... (Ctrl+C to exit)")
	wg.Wait()
}

// recvStageBpSocket: BP Socketから連続的にバンドルを受信してURLを抽出
func recvStageBpSocket(dataChan <-chan []byte, urlChan chan<- CrawlRequest) {
	for data := range dataChan {
		log.Printf(">>> Recv Stage: Received bundle (%d bytes)", len(data))

		// JSONをパース
		targetURL, reqID, err := bpsocket.ParseDTNRequest(data)
		if err != nil {
			log.Printf("⚠️  Parse error: %v", err)
			// エラーレスポンスを生成
			errorURL := fmt.Sprintf("error://invalid-request/%s", url.QueryEscape(err.Error()))
			urlChan <- CrawlRequest{RequestID: reqID, URL: errorURL, Depth: 0}
			continue
		}

		log.Printf("🔄 NEW REQUEST: %s (ID: %s)", targetURL, reqID)
		urlChan <- CrawlRequest{RequestID: reqID, URL: targetURL, Depth: 0}
	}
}

// fetchWorkerBpSocket: HTTPリクエストを実行
func fetchWorkerBpSocket(urlChan <-chan CrawlRequest, bpResChan chan<- BpResponse) {
	client := http.Client{Timeout: 30 * time.Second}

	for reqInfo := range urlChan {
		targetURL := reqInfo.URL
		reqID := reqInfo.RequestID
		depth := reqInfo.Depth

		// エラーURLの検出
		if strings.HasPrefix(targetURL, "error://") {
			errRes := BpResponse{
				RequestID:     reqID,
				StatusCode:    400,
				Headers:       map[string][]string{"Content-Type": {"text/plain"}},
				Body:          base64.StdEncoding.EncodeToString([]byte("Error: Invalid or incomplete HTTP request")),
				ContentType:   "text/plain",
				ContentLength: int64(len("Error: Invalid or incomplete HTTP request")),
				Depth:         0,
			}
			bpResChan <- errRes
			log.Printf("❌ Sent 400 Bad Request for: %s", targetURL)
			continue
		}

		// 再訪問チェック
		visitedMutex.Lock()
		if visitedURLs[targetURL] {
			visitedMutex.Unlock()
			continue
		}
		visitedURLs[targetURL] = true
		visitedMutex.Unlock()

		log.Printf("🕸️  Fetching: %s", targetURL)

		// HTTPリクエストの実行
		req, err := http.NewRequestWithContext(context.Background(), "GET", targetURL, nil)
		if err != nil {
			log.Printf("⚠️  Request creation error (%s): %v", targetURL, err)
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("⚠️  HTTP request error (%s): %v", targetURL, err)
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("⚠️  Body read error (%s): %v", targetURL, err)
			continue
		}

		bpRes := BpResponse{
			RequestID:     reqID,
			StatusCode:    resp.StatusCode,
			Headers:       resp.Header,
			Body:          base64.StdEncoding.EncodeToString(bodyBytes),
			ContentType:   resp.Header.Get("Content-Type"),
			ContentLength: resp.ContentLength,
			Depth:         depth,
		}
		bpRes.Headers["X-Original-URL"] = []string{targetURL}

		bpResChan <- bpRes
		log.Printf("✅ Fetched: %s (Status: %d, Size: %d bytes)", targetURL, bpRes.StatusCode, len(bodyBytes))
	}
}

// saveAndRecurseWorkerBpSocket: 再帰リンクの処理とsendChanへの転送
func saveAndRecurseWorkerBpSocket(bpResChan <-chan BpResponse, urlChan chan<- CrawlRequest, sendChan chan<- BpResponse) {
	for bpRes := range bpResChan {
		originalURL := bpRes.Headers["X-Original-URL"][0]

		// エラーレスポンスでも送信キューに追加
		sendChan <- bpRes

		// エラーレスポンスの場合、再帰処理は行わない
		if bpRes.StatusCode == 400 {
			log.Printf("⚠️  Skipping recursion for error response")
			continue
		}

		// 再帰リンクの処理
		currentDepth := bpRes.Depth
		if currentDepth < maxDepth {
			links := extractLinksBpSocket(bpRes, originalURL)
			for _, link := range links {
				visitedMutex.Lock()
				if !visitedURLs[link] {
					visitedMutex.Unlock()
					urlChan <- CrawlRequest{RequestID: bpRes.RequestID, URL: link, Depth: currentDepth + 1}
					log.Printf("🔗 Link Found (Depth %d): %s", currentDepth+1, link)
				} else {
					visitedMutex.Unlock()
				}
			}
		}
	}
	close(sendChan)
}

// sendWorkerBpSocket: BP Socketでレスポンスを送信
func sendWorkerBpSocket(bpResChan <-chan BpResponse, sender *bpsocket.BpSender, workerID int) {
	for bpRes := range bpResChan {
		log.Printf("🚀 [Worker %d] Sending response (ID: %s, Status: %d)", workerID, bpRes.RequestID, bpRes.StatusCode)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := sender.Send(ctx, bpRes)
		cancel()

		if err != nil {
			log.Printf("❌ [Worker %d] Send error: %v", workerID, err)
		} else {
			log.Printf("✅ [Worker %d] Response sent successfully (ID: %s)", workerID, bpRes.RequestID)
		}
	}
}

// extractLinksBpSocket: BpResponseからHTMLリンクを抽出
func extractLinksBpSocket(bpRes BpResponse, baseURLStr string) []string {
	var links []string

	if !strings.HasPrefix(bpRes.ContentType, "text/html") {
		return links
	}

	bodyBytes, err := base64.StdEncoding.DecodeString(bpRes.Body)
	if err != nil {
		log.Printf("⚠️  Base64 decode error: %v", err)
		return links
	}
	htmlContent := string(bodyBytes)

	baseURL, err := url.Parse(baseURLStr)
	if err != nil {
		log.Printf("⚠️  URL parse error: %v", err)
		return links
	}

	matches := linkRegex.FindAllStringSubmatch(htmlContent, -1)
	for _, match := range matches {
		if len(match) > 1 {
			relativeURL := match[1]
			resolvedURL, err := baseURL.Parse(relativeURL)

			if err == nil && resolvedURL.Host == baseURL.Host && resolvedURL.Scheme != "" {
				resolvedURL.RawQuery = ""
				resolvedURL.Fragment = ""
				links = append(links, resolvedURL.String())
			}
		}
	}
	return links
}
