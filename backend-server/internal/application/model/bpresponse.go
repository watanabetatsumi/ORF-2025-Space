package model

import "io"

// BpResponse HTTPレスポンスに必要な情報を格納する構造体
type BpResponse struct {
	// StatusCode HTTPステータスコード（200, 404, 500など）
	StatusCode int `json:"status_code"`

	// Headers HTTPヘッダー（キーと値のマップ）
	Headers map[string][]string `json:"headers"`

	// Body レスポンスボディ（バイト配列）
	Body []byte `json:"body"`

	// ContentType Content-Typeヘッダーの値
	ContentType string `json:"content_type,omitempty"`

	// ContentLength Content-Lengthヘッダーの値
	ContentLength int64 `json:"content_length,omitempty"`
}

// GetBodyReader レスポンスボディをio.Readerとして返す
func (br *BpResponse) GetBodyReader() io.Reader {
	if len(br.Body) == 0 {
		return nil
	}
	return &bodyReader{data: br.Body}
}
