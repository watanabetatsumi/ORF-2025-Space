package service

import (
	"github.com/watanabetatsumi/backend-server/internal/application/interface/gateway"
)

type BpService struct {
	bpgateway gateway.BpGateway
}
