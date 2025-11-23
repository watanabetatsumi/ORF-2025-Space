// socket.go - AF_BPソケットの基本操作（作成、送受信、クローズ）
package bpsocket

import (
	"fmt"
	"syscall"
)

type BpSocket struct {
	fd        int
	localAddr *SockaddrBP
}

func NewBpSocket(localNodeNum, localSvcNum uint64) (*BpSocket, error) {
	fd, err := syscall.Socket(AF_BP, SOCK_DGRAM, BP_PROTO)
	if err != nil {
		return nil, fmt.Errorf("socket creation failed: %w", err)
	}

	localAddr := NewSockaddrBP(localNodeNum, localSvcNum)

	err = bind(int(fd), localAddr)
	if err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("bind failed %s: %w", localAddr.String(), err)
	}

	return &BpSocket{
		fd:        int(fd),
		localAddr: localAddr,
	}, nil
}

func (s *BpSocket) Send(data []byte, remoteNodeNum, remoteSvcNum uint64) error {
	remoteAddr := NewSockaddrBP(remoteNodeNum, remoteSvcNum)
	err := sendto(s.fd, data, remoteAddr)
	if err != nil {
		return fmt.Errorf("sendto %s failed: %w", remoteAddr.String(), err)
	}
	return nil
}

func (s *BpSocket) Recv(buf []byte) (int, *SockaddrBP, error) {
	n, fromAddr, err := recvfrom(s.fd, buf)
	if err != nil {
		return 0, nil, fmt.Errorf("recvfrom failed: %w", err)
	}
	return n, fromAddr, nil
}

func (s *BpSocket) Close() error {
	if s.fd >= 0 {
		return closeFd(s.fd)
	}
	return nil
}

func (s *BpSocket) LocalAddr() *SockaddrBP {
	return s.localAddr
}

