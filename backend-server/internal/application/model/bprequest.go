package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// BpRequest HTTPリクエストに必要な情報を格納する構造体
type BpRequest struct {
	// Method HTTPメソッド（GET, POST, PUT, DELETE, PATCHなど）
	Method string `json:"method"`

	// URL 転送先のURL
	URL string `json:"url"`

	// Headers HTTPヘッダー（キーと値のマップ）
	Headers map[string][]string `json:"headers"`

	// Body リクエストボディ（バイト配列）
	Body []byte `json:"body,omitempty"`

	// ContentType Content-Typeヘッダーの値
	ContentType string `json:"content_type,omitempty"`

	// ContentLength Content-Lengthヘッダーの値
	ContentLength int64 `json:"content_length,omitempty"`
}

// ParseURL URL文字列を解析してurl.URLを返す
func (br *BpRequest) ParseURL() (*url.URL, error) {
	return url.Parse(br.URL)
}

// ValidateURL URLが有効かどうかを検証する（domain層のロジック）
func (br *BpRequest) ValidateURL() error {
	if br.URL == "" {
		return fmt.Errorf("URL is empty")
	}
	_, err := url.Parse(br.URL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	return nil
}

// GetBodyReader リクエストボディをio.Readerとして返す
func (br *BpRequest) GetBodyReader() io.Reader {
	if len(br.Body) == 0 {
		return nil
	}
	return &bodyReader{data: br.Body}
}

// GetPath URLからPathを取得する
func (br *BpRequest) GetPath() (string, error) {
	parsedURL, err := br.ParseURL()
	if err != nil {
		return "", err
	}
	return parsedURL.Path, nil
}

// SetHeaders HTTPリクエストにヘッダーを設定する
func (br *BpRequest) SetHeaders(httpReq *http.Request) {
	// ヘッダーをコピー
	for key, values := range br.Headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}

	// Content-TypeとContent-Lengthを設定
	if br.ContentType != "" {
		httpReq.Header.Set("Content-Type", br.ContentType)
	}
	if br.ContentLength > 0 {
		httpReq.ContentLength = br.ContentLength
	}
}

// IsCacheable このリクエストがキャッシュ可能かどうかを判定する
// 以下の場合はキャッシュしない:
// - GET以外のメソッド（POST, PUT, DELETE, PATCHなど）
// - 認証が必要なページ（Authorizationヘッダーがある場合、ユーザーごとにキャッシュを分けるか、キャッシュしない）
// - セッション情報を含むページ（Cookieにセッション情報がある場合）
func (br *BpRequest) IsCacheable() bool {
	// GETリクエストのみキャッシュ可能
	if br.Method != "GET" {
		return false
	}

	// 認証ヘッダーがある場合は、ユーザーごとにキャッシュを分ける必要がある
	// この場合はキャッシュ可能だが、キーに認証情報を含める必要がある
	// または、認証が必要なページはキャッシュしないという選択肢もある
	// ここでは、認証ヘッダーがある場合はキャッシュ可能とする（キーに含める）

	// Cookieにセッション情報がある場合は、動的コンテンツの可能性が高いためキャッシュしない
	if cookies, ok := br.Headers["Cookie"]; ok {
		for _, cookie := range cookies {
			// セッションIDや認証情報を含むCookieがある場合はキャッシュしない
			if strings.Contains(strings.ToLower(cookie), "session") ||
				strings.Contains(strings.ToLower(cookie), "auth") ||
				strings.Contains(strings.ToLower(cookie), "token") {
				return false
			}
		}
	}

	return true
}

// IsUserSpecific このリクエストがユーザー固有のコンテンツかどうかを判定する
// 認証情報やセッション情報がある場合は、ユーザーごとにキャッシュを分ける必要がある
func (br *BpRequest) IsUserSpecific() bool {
	// Authorizationヘッダーがある場合
	if _, ok := br.Headers["Authorization"]; ok {
		return true
	}

	// Cookieに認証情報がある場合
	if cookies, ok := br.Headers["Cookie"]; ok {
		for _, cookie := range cookies {
			if strings.Contains(strings.ToLower(cookie), "session") ||
				strings.Contains(strings.ToLower(cookie), "auth") ||
				strings.Contains(strings.ToLower(cookie), "token") {
				return true
			}
		}
	}

	return false
}

// GenerateCacheKey リクエストからキャッシュキーを生成する
// メソッド、URL、重要なヘッダーから一意のキーを生成
// ユーザー固有のコンテンツの場合は、認証情報もキーに含める
func (br *BpRequest) GenerateCacheKey() string {
	// 基本的なキー: メソッド + URL
	baseKey := fmt.Sprintf("%s:%s", br.Method, br.URL)

	// 重要なヘッダーをソートして追加
	var headerParts []string

	// 認証情報がある場合は、ユーザーごとにキャッシュを分けるために含める
	if br.IsUserSpecific() {
		if auth, ok := br.Headers["Authorization"]; ok {
			headerParts = append(headerParts, fmt.Sprintf("auth:%s", strings.Join(auth, ",")))
		}
		if cookies, ok := br.Headers["Cookie"]; ok {
			// Cookieから認証関連の情報のみを抽出
			for _, cookie := range cookies {
				if strings.Contains(strings.ToLower(cookie), "session") ||
					strings.Contains(strings.ToLower(cookie), "auth") ||
					strings.Contains(strings.ToLower(cookie), "token") {
					headerParts = append(headerParts, fmt.Sprintf("cookie:%s", cookie))
				}
			}
		}
	}

	// その他の重要なヘッダー
	importantHeaders := []string{"Accept", "Accept-Language"}
	for _, headerName := range importantHeaders {
		if values, ok := br.Headers[headerName]; ok {
			headerParts = append(headerParts, fmt.Sprintf("%s:%s", headerName, strings.Join(values, ",")))
		}
	}

	// ヘッダー部分を追加
	if len(headerParts) > 0 {
		sort.Strings(headerParts)
		baseKey += ":" + strings.Join(headerParts, "|")
	}

	// SHA256ハッシュでキーを短縮（Redisのキー長制限対策）
	hash := sha256.Sum256([]byte(baseKey))
	return "bp:cache:" + hex.EncodeToString(hash[:])
}

// GenerateCachePathInfo レスポンスのContentTypeからキャッシュパス情報を生成する（domain層のロジック）
func (br *BpRequest) GenerateCachePathInfo(responseContentType string) (*CachePathInfo, error) {
	cacheKey := br.GenerateCacheKey()
	return GenerateCachePathInfo(br.URL, responseContentType, cacheKey)
}

// bodyReader バイト配列をio.Readerとして扱うためのヘルパー
type bodyReader struct {
	data []byte
	pos  int
}

func (r *bodyReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// Close io.ReadCloserインターフェースを満たすためのダミーメソッド
func (r *bodyReader) Close() error {
	return nil
}
