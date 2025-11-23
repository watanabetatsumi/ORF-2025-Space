//go:build windows
// +build windows

// syscall_windows.go - Windows環境用スタブ（bp-socketはサポートしてない）
package bpsocket

import (
	"fmt"
	"syscall"
)

func closeFd(fd int) error {
	return syscall.Close(syscall.Handle(fd))
}

func bind(fd int, addr *SockaddrBP) error {
	return fmt.Errorf("bp-socket not supported on Windows")
}

func sendto(fd int, data []byte, remoteAddr *SockaddrBP) error {
	return fmt.Errorf("bp-socket not supported on Windows")
}

func recvfrom(fd int, buf []byte) (int, *SockaddrBP, error) {
	return 0, nil, fmt.Errorf("bp-socket not supported on Windows")
}
