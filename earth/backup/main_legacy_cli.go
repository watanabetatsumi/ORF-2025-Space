package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DTNJsonRequest DTN経由で受信するリクエスト構造体
type DTNJsonRequest struct {
	RequestID string              `json:"request_id"`
	Method    string              `json:"method"`
	URL       string              `json:"url"`
	Headers   map[string][]string `json:"headers"`
	Body      string              `json:"body"` // Base64エンコード
}

// CrawlRequest 内部処理用のクロールリクエスト構造体
type CrawlRequest struct {
	RequestID string // ★追加
	URL       string
	Depth     int
}

// BpResponse HTTPレスポンスに必要な情報を格納する構造体
type BpResponse struct {
	RequestID     string              `json:"request_id"` // ★追加
	StatusCode    int                 `json:"status_code"`
	Headers       map[string][]string `json:"headers"`
	Body          string              `json:"body"` // Base64エンコード
	ContentType   string              `json:"content_type,omitempty"`
	ContentLength int64               `json:"content_length,omitempty"`
	Depth         int                 `json:"-"` // 内部管理用 (JSONには含めない)
}

// 共通リソース
var (
	// URLの重複訪問を防ぐためのセット
	visitedURLs  = make(map[string]bool)
	visitedMutex sync.Mutex
	// HTMLの<a>タグのhref属性を抽出する正規表現
	linkRegex = regexp.MustCompile(`(?i)<a\s+(?:[^>]*?\s+)?href=["']?([^"'>\s]+)["']?`)

	// JSON出力ディレクトリ
	outputDir = "response_json"

	// 処理の最大再帰深さ
	maxDepth = 2

	// bprecvfileが生成するファイル名のプレフィックスと実行間隔
	recvFilePrefix = "testfile"
	watchInterval  = 2 * time.Second // bprecvfile再実行前の待機時間
)

func main() {
	fmt.Println("=== Bp Gateway Processor 起動 (Modern Go & Auto-Clean Mode) ===")

	// ディレクトリの準備
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Printf("出力ディレクトリの作成に失敗しました: %v\n", err)
		return
	}

	// パイプライン用チャネルの作成
	urlChan := make(chan CrawlRequest, 100)
	bpResChan := make(chan BpResponse, 100)
	fileChan := make(chan string, 100)

	var wg sync.WaitGroup

	// --- 1. Recv Stage (bprecvfile実行、終了待ち、ファイル処理・削除) ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		recvStage(urlChan)
	}()

	// --- 2. Fetch Stage (HTTPリクエスト実行) ---
	const fetchWorkers = 5
	for i := 0; i < fetchWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fetchWorker(urlChan, bpResChan)
		}()
	}

	// --- 3. Save & Recurse Stage (JSON保存と再帰) ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		saveAndRecurseWorker(bpResChan, urlChan, fileChan)
	}()

	// --- 4. Send Stage (bpsendfile実行) ---
	const sendWorkers = 3
	for i := 0; i < sendWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sendWorker(fileChan)
		}()
	}

	fmt.Println("プロセッサは起動中... (Ctrl+Cで終了)")
	wg.Wait()
}

// ------------------------------
// --- ステージの実装 ---
// ------------------------------

// recvStage: bprecvfileを実行し、生成されたファイルを処理して削除する
func recvStage(urlChan chan<- CrawlRequest) {
	cmdStr := "bprecvfile ipn:150.1 1"
	watchDir := "."
	// ※注意: IONの設定に合わせてパスを変更してください。カレントなら "."

	// processedFiles マップは削除しました。
	// ファイル処理完了時に即削除するフローに変更したため不要です。

	for {
		fmt.Printf(">>> Recv Stage: bprecvfile を実行します: %s\n", cmdStr)

		// 1. bprecvfileを実行し、終了を待つ
		parts := strings.Fields(cmdStr)
		if len(parts) == 0 {
			time.Sleep(5 * time.Second)
			continue
		}

		// タイムアウトを設定して実行（データが来ない場合にずっと止まるのを防ぐ）
		// 10秒待って来なければタイムアウトとしてループを回す
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
		output, err := cmd.CombinedOutput()
		cancel() // リソース解放

		// タイムアウトの判定
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Println(":hourglass_flowing_sand: 受信待機中... (タイムアウトにより再試行)")
			continue
		}

		if err != nil {
			fmt.Printf(":x: bprecvfile実行エラー: %v\n", err)
			fmt.Printf("   -> 出力:\n%s\n", string(output))
			time.Sleep(5 * time.Second)
			continue
		}
		fmt.Println(":white_check_mark: bprecvfile 実行完了。生成ファイルを確認します。")

		// 2. コマンド実行後のファイルチェック
		// Go 1.16+ 対応: os.ReadDir を使用
		files, err := os.ReadDir(watchDir)
		if err != nil {
			fmt.Printf(":warning: ディレクトリ読み取りエラー: %v\n", err)
			time.Sleep(watchInterval)
			continue
		}

		newlyProcessed := false

		for _, file := range files {
			filename := file.Name()
			filePath := filepath.Join(watchDir, filename)

			// ディレクトリはスキップ
			if file.IsDir() {
				continue
			}

			// プレフィックスのチェック (testfile...)
			if !strings.HasPrefix(filename, recvFilePrefix) {
				continue
			}

			// JSONファイルからURLとIDをパース
			fullURL, reqID, err := parseRequestFile(filePath)

			if err != nil {
				// :x: 解析失敗時はエラー報告し、ファイルを削除する
				errorURL := fmt.Sprintf("error://invalid-request/%s", url.QueryEscape(filename))
				urlChan <- CrawlRequest{URL: errorURL, Depth: 0}
				fmt.Printf(":warning: INVALID REQUEST -> %s (Error: %s)\n", filename, err)

				// ★ここで削除
				if rmErr := os.Remove(filePath); rmErr != nil {
					fmt.Printf(":warning: 削除失敗 (Error file): %v\n", rmErr)
				} else {
					fmt.Printf(":wastebasket: 解析不能ファイルを削除しました: %s\n", filename)
				}
				continue
			}

			// 成功したURLを次のステージへ
			urlChan <- CrawlRequest{RequestID: reqID, URL: fullURL, Depth: 0}
			fmt.Printf(":rotating_light: NEW REQUEST URL -> %s (Source: %s)\n", fullURL, filename)

			// ★処理完了後にファイルを削除
			if rmErr := os.Remove(filePath); rmErr != nil {
				fmt.Printf(":warning: ファイル削除失敗: %v\n", rmErr)
			} else {
				fmt.Printf(":wastebasket: 処理済みファイルを削除しました: %s\n", filename)
			}

			newlyProcessed = true
		}

		if !newlyProcessed {
			fmt.Println(":mag: 今回の実行で処理対象ファイルはありませんでした。")
		}

		// 3. 次の実行までの待機
		time.Sleep(watchInterval)
	}
}

// fetchWorker: urlChanからURLを受け取り、HTTPリクエストを実行し、結果をbpResChanに送る
func fetchWorker(urlChan <-chan CrawlRequest, bpResChan chan<- BpResponse) {
	client := http.Client{Timeout: 30 * time.Second}

	for reqInfo := range urlChan {
		targetURL := reqInfo.URL
		reqID := reqInfo.RequestID // ★IDを取得
		depth := reqInfo.Depth

		// 1. エラーURLの検出
		if strings.HasPrefix(targetURL, "error://") {
			parsedURL, _ := url.Parse(targetURL)
			originalFilename := parsedURL.Host

			// 400 Bad Request を示すエラーレスポンスを構築
			errRes := BpResponse{
				StatusCode:    400, // Bad Request
				Headers:       map[string][]string{"Content-Type": {"text/plain"}},
				Body:          base64.StdEncoding.EncodeToString([]byte("Error: Invalid or incomplete HTTP request received (Missing GET line or Host header).")),
				ContentType:   "text/plain",
				ContentLength: int64(len("Error: Invalid or incomplete HTTP request received (Missing GET line or Host header).")),
				Depth:         0,
			}
			errRes.Headers["X-Original-URL"] = []string{fmt.Sprintf("ERROR_FROM_FILE: %s", originalFilename)}

			bpResChan <- errRes
			fmt.Printf(":x: Sent 400 Bad Request for invalid request from: %s\n", originalFilename)
			continue
		}

		// 2. 再訪問チェック
		visitedMutex.Lock()
		if visitedURLs[targetURL] {
			visitedMutex.Unlock()
			continue
		}
		visitedURLs[targetURL] = true
		visitedMutex.Unlock()

		fmt.Printf(":spider_web: Fetching: %s\n", targetURL)

		// 3. HTTPリクエストの実行
		req, err := http.NewRequestWithContext(context.Background(), "GET", targetURL, nil)
		if err != nil {
			fmt.Printf(":warning: リクエスト作成エラー (%s): %v\n", targetURL, err)
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf(":warning: HTTPリクエストエラー (%s): %v\n", targetURL, err)
			continue
		}

		// Go 1.16+ 対応: io.ReadAll を使用
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			fmt.Printf(":warning: ボディ読み込みエラー (%s): %v\n", targetURL, err)
			continue
		}

		bpRes := BpResponse{
			RequestID:     reqID, // ★レスポンスにセット
			StatusCode:    resp.StatusCode,
			Headers:       resp.Header,
			Body:          base64.StdEncoding.EncodeToString(bodyBytes),
			ContentType:   resp.Header.Get("Content-Type"),
			ContentLength: resp.ContentLength,
			Depth:         depth,
		}

		bpRes.Headers["X-Original-URL"] = []string{targetURL}

		bpResChan <- bpRes
		fmt.Printf(":white_check_mark: Fetched & Sent to Save: %s (Status: %d)\n", targetURL, bpRes.StatusCode)
	}
}

// saveAndRecurseWorker: bpResChanから結果を受け取り、JSON保存と再帰リンクの処理を行う
func saveAndRecurseWorker(bpResChan <-chan BpResponse, urlChan chan<- CrawlRequest, fileChan chan<- string) {
	for bpRes := range bpResChan {
		// エラーレスポンスの場合、再帰処理は行わない
		originalURL := bpRes.Headers["X-Original-URL"][0]
		if bpRes.StatusCode == 400 && strings.HasPrefix(originalURL, "ERROR_FROM_FILE") {
			// JSONファイルへの書き込み（エラーレスポンスも含む）
			filename := saveJSON(bpRes, originalURL)
			if filename != "" {
				fileChan <- filename
			}
			continue
		}

		// 1. JSONファイルへの書き込み
		filename := saveJSON(bpRes, originalURL)
		if filename != "" {
			fileChan <- filename // 成功したら次のSendステージへ
		}

		// 2. 再帰リンクの処理
		currentDepth := bpRes.Depth
		if currentDepth < maxDepth {
			links := extractLinks(bpRes, originalURL)
			for _, link := range links {
				// 再訪問を回避しつつ、urlChanに戻す
				visitedMutex.Lock()
				if !visitedURLs[link] {
					visitedMutex.Unlock()
					urlChan <- CrawlRequest{RequestID: bpRes.RequestID, URL: link, Depth: currentDepth + 1}
					fmt.Printf(":link: Link Found (Depth %d): %s\n", currentDepth+1, link)
				} else {
					visitedMutex.Unlock()
				}
			}
		}
	}
}

// sendWorker: fileChanからJSONファイル名を受け取り、bpsendfileを実行する
func sendWorker(fileChan <-chan string) {
	for filename := range fileChan {
		// ION-DTNコマンド
		cmdStr := fmt.Sprintf("bpsendfile ipn:150.2 ipn:149.1 %s", filename)

		parts := strings.Fields(cmdStr)
		if len(parts) == 0 {
			continue
		}

		cmd := exec.Command(parts[0], parts[1:]...)
		output, err := cmd.CombinedOutput()

		if err != nil {
			fmt.Printf(":x: bpsendfile実行エラー (%s): %v\n", cmdStr, err)
			fmt.Printf("   -> 出力:\n%s\n", string(output))
		} else {
			fmt.Printf(":rocket: bpsendfile成功: %s\n", cmdStr)
		}
	}
}

// ------------------------------
// --- ヘルパー関数 ---
// ------------------------------

// parseRequestFile: 生成されたJSONファイルの内容からURLを抽出する
func parseRequestFile(filePath string) (targetURL string, reqID string, err error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", "", fmt.Errorf("ファイル読み込みエラー: %w", err)
	}

	var dtnReq DTNJsonRequest
	if err := json.Unmarshal(data, &dtnReq); err != nil {
		return "", "", fmt.Errorf("JSONパースエラー: %w", err)
	}

	if dtnReq.URL == "" {
		return "", "", fmt.Errorf("URLが空です")
	}

	return dtnReq.URL, dtnReq.RequestID, nil
}

// saveJSON: BpResponseをJSONファイルとして保存する
func saveJSON(bpRes BpResponse, originalURL string) string {
	jsonData, err := json.MarshalIndent(bpRes, "", "  ")
	if err != nil {
		fmt.Printf("JSONエンコードエラー (%s): %v\n", originalURL, err)
		return ""
	}

	// ファイル名をURLから生成
	sanitizedName := strings.NewReplacer(
		"http://", "", "https://", "", "/", "_",
		"?", "_", "&", "_", "=", "_", ":", "_", ".", "_",
	).Replace(originalURL) + ".json"

	// エラーファイル名は特別にプレフィックスを付加
	if bpRes.StatusCode == 400 {
		sanitizedName = "ERROR_" + sanitizedName
	}

	filename := filepath.Join(outputDir, sanitizedName)

	// Go 1.16+ 対応: os.WriteFile を使用
	if err := os.WriteFile(filename, jsonData, 0644); err != nil {
		fmt.Printf("ファイル書き込みエラー (%s): %v\n", filename, err)
		return ""
	}
	return filename
}

// extractLinks: BpResponseからHTMLリンクを抽出し、絶対URLに変換して返す
func extractLinks(bpRes BpResponse, baseURLStr string) []string {
	var links []string

	if !strings.HasPrefix(bpRes.ContentType, "text/html") {
		return links
	}

	bodyBytes, err := base64.StdEncoding.DecodeString(bpRes.Body)
	if err != nil {
		fmt.Printf(":warning: Base64デコードエラー: %v\n", err)
		return links
	}
	htmlContent := string(bodyBytes)

	baseURL, err := url.Parse(baseURLStr)
	if err != nil {
		fmt.Printf(":warning: URLパースエラー: %v\n", err)
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
