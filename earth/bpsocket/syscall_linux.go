//go:build linux
// +build linux

// syscall_linux.go - Linux環境でのAF_BPソケットシステムコール実装
package bpsocket

import (
	"fmt"
	"syscall"
	"unsafe"
)

func closeFd(fd int) error {
	return syscall.Close(fd)
}

func bind(fd int, addr *SockaddrBP) error {
	rawAddr := (*syscall.RawSockaddrAny)(unsafe.Pointer(addr))
	_, _, errno := syscall.Syscall(
		syscall.SYS_BIND,
		uintptr(fd),
		uintptr(unsafe.Pointer(rawAddr)),
		uintptr(unsafe.Sizeof(*addr)),
	)
	if errno != 0 {
		return fmt.Errorf("bind syscall error: %v", errno)
	}
	return nil
}

func sendto(fd int, data []byte, remoteAddr *SockaddrBP) error {
	rawAddr := (*syscall.RawSockaddrAny)(unsafe.Pointer(remoteAddr))
	_, _, errno := syscall.Syscall6(
		syscall.SYS_SENDTO,
		uintptr(fd),
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		0,
		uintptr(unsafe.Pointer(rawAddr)),
		uintptr(unsafe.Sizeof(*remoteAddr)),
	)
	if errno != 0 {
		return fmt.Errorf("sendto syscall error: %v", errno)
	}
	return nil
}

func recvfrom(fd int, buf []byte) (int, *SockaddrBP, error) {
	var fromAddr SockaddrBP
	fromLen := uint32(unsafe.Sizeof(fromAddr))

	n, _, errno := syscall.Syscall6(
		syscall.SYS_RECVFROM,
		uintptr(fd),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0,
		uintptr(unsafe.Pointer(&fromAddr)),
		uintptr(unsafe.Pointer(&fromLen)),
	)
	if errno != 0 {
		return 0, nil, fmt.Errorf("recvfrom syscall error: %v", errno)
	}

	return int(n), &fromAddr, nil
}
