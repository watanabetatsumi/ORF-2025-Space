package service

import (
	"github.com/watanabetatsumi/backend-server/internal/application/interface/gateway"
)

type BpService struct {
	bpgateway gateway.BpGateway
}

func NewBpService(bpgateway gateway.BpGateway) *BpService {
	return &BpService{
		bpgateway: bpgateway,
	}
}

func (bs *BpService) GetContent(domain string) (string, error) {
	return bs.bpgateway.GetContent(domain)
}
