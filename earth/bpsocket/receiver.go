// receiver.go - BP Socket連続受信ゲートウェイ
package bpsocket

import (
	"encoding/json"
	"fmt"
	"log"
	"runtime"
)

const maxBundleSize = 4 * 1024 * 1024

// BpReceiver BP Socketで連続的にバンドルを受信する
type BpReceiver struct {
	socket   *BpSocket
	dataChan chan []byte
	stopChan chan struct{}
}

// NewBpReceiver 受信専用のBP Socketを作成
func NewBpReceiver(localNodeNum, localSvcNum uint64) (*BpReceiver, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("bp-socket is only supported on Linux (current OS: %s)", runtime.GOOS)
	}

	socket, err := NewBpSocket(localNodeNum, localSvcNum)
	if err != nil {
		return nil, fmt.Errorf("failed to create BP socket: %w", err)
	}

	log.Printf("[BpReceiver] Listening on %s", socket.LocalAddr().String())

	return &BpReceiver{
		socket:   socket,
		dataChan: make(chan []byte, 100),
		stopChan: make(chan struct{}),
	}, nil
}

// Start 受信ループを開始
func (r *BpReceiver) Start() {
	go r.receiveLoop()
}

// GetDataChannel 受信データを取得するチャネル
func (r *BpReceiver) GetDataChannel() <-chan []byte {
	return r.dataChan
}

// Close ソケットをクローズして受信を停止
func (r *BpReceiver) Close() error {
	close(r.stopChan)
	return r.socket.Close()
}

func (r *BpReceiver) receiveLoop() {
	buf := make([]byte, maxBundleSize)

	for {
		select {
		case <-r.stopChan:
			log.Println("[BpReceiver] Receive loop stopped")
			close(r.dataChan)
			return
		default:
		}

		n, fromAddr, err := r.socket.Recv(buf)
		if err != nil {
			select {
			case <-r.stopChan:
				return
			default:
				log.Printf("[BpReceiver] Recv error: %v", err)
				continue
			}
		}

		if n >= maxBundleSize {
			log.Printf("[BpReceiver] WARNING: Received %d bytes (buffer limit), possible truncation", n)
		}

		log.Printf("[BpReceiver] Received %d bytes from %s", n, fromAddr.String())

		// データをコピーしてチャネルに送信
		data := make([]byte, n)
		copy(data, buf[:n])

		select {
		case r.dataChan <- data:
			log.Printf("[BpReceiver] Bundle dispatched to processing pipeline")
		default:
			log.Printf("[BpReceiver] WARNING: Data channel full, dropping bundle")
		}
	}
}

// ParseDTNRequest バンドルペイロードからDTNJsonRequestをパース
func ParseDTNRequest(data []byte) (url string, reqID string, err error) {
	var req struct {
		RequestID string `json:"request_id"`
		URL       string `json:"url"`
	}

	if err := json.Unmarshal(data, &req); err != nil {
		return "", "", fmt.Errorf("JSON parse error: %w", err)
	}

	if req.URL == "" {
		return "", "", fmt.Errorf("URL is empty")
	}

	return req.URL, req.RequestID, nil
}
