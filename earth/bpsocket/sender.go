// sender.go - BP Socket送信ゲートウェイ
package bpsocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime"
)

// BpSender BP Socketでバンドルを送信する
type BpSender struct {
	socket        *BpSocket
	remoteNodeNum uint64
	remoteSvcNum  uint64
}

// NewBpSender 送信専用のBP Socketを作成
func NewBpSender(localNodeNum, localSvcNum, remoteNodeNum, remoteSvcNum uint64) (*BpSender, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("bp-socket is only supported on Linux (current OS: %s)", runtime.GOOS)
	}

	socket, err := NewBpSocket(localNodeNum, localSvcNum)
	if err != nil {
		return nil, fmt.Errorf("failed to create BP socket: %w", err)
	}

	log.Printf("[BpSender] Created socket %s -> ipn:%d.%d",
		socket.LocalAddr().String(), remoteNodeNum, remoteSvcNum)

	return &BpSender{
		socket:        socket,
		remoteNodeNum: remoteNodeNum,
		remoteSvcNum:  remoteSvcNum,
	}, nil
}

// Send バンドルを送信
func (s *BpSender) Send(ctx context.Context, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("JSON marshal error: %w", err)
	}

	if len(jsonData) > maxBundleSize {
		return fmt.Errorf("bundle size %d exceeds max %d", len(jsonData), maxBundleSize)
	}

	log.Printf("[BpSender] Sending %d bytes to ipn:%d.%d", len(jsonData), s.remoteNodeNum, s.remoteSvcNum)

	if err := s.socket.Send(jsonData, s.remoteNodeNum, s.remoteSvcNum); err != nil {
		return fmt.Errorf("socket send error: %w", err)
	}

	log.Printf("[BpSender] Bundle sent successfully")
	return nil
}

// Close ソケットをクローズ
func (s *BpSender) Close() error {
	return s.socket.Close()
}
