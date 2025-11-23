// address.go - IPNアドレス（ipn:node.service）の構造体と変換処理
package bpsocket

import (
	"fmt"
	"unsafe"
)

// カーネルモジュールのsockaddr_bp構造体に対応
// C構造体:
//
//	struct sockaddr_bp {
//	  sa_family_t bp_family;  // uint16
//	  bp_scheme_t bp_scheme;  // int32 (enum)
//	  union {
//	    struct {
//	      uint32_t node_id;
//	      uint32_t service_id;
//	    } ipn;
//	  } bp_addr;
//	};
type SockaddrBP struct {
	Family  uint16 // bp_family (AF_BP = 28)
	Scheme  int32  // bp_scheme (BP_SCHEME_IPN = 1)
	NodeNum uint32 // bp_addr.ipn.node_id
	SvcNum  uint32 // bp_addr.ipn.service_id
}

func (sa *SockaddrBP) ToBytes() []byte {
	size := int(unsafe.Sizeof(*sa))
	ptr := unsafe.Pointer(sa)
	return (*[unsafe.Sizeof(SockaddrBP{})]byte)(ptr)[:size:size]
}

func NewSockaddrBP(nodeNum, svcNum uint64) *SockaddrBP {
	return &SockaddrBP{
		Family:  AF_BP,
		Scheme:  BP_SCHEME_IPN,
		NodeNum: uint32(nodeNum),
		SvcNum:  uint32(svcNum),
	}
}

func (sa *SockaddrBP) String() string {
	return fmt.Sprintf("ipn:%d.%d", sa.NodeNum, sa.SvcNum)
}
