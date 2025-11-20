package middleware

import (
	"net"

	"github.com/watanabetatsumi/ORF-2025-Space/backend-server/internal/middleware/module"
)

type MiddlewarePlugins struct {
	SSLBumpHandler *module.SSLBumpHandler
}

func NewMiddlewarePlugins(sslBumpHandler *module.SSLBumpHandler) *MiddlewarePlugins {
	return &MiddlewarePlugins{
		SSLBumpHandler: sslBumpHandler,
	}
}

type SSLBumpHandler interface {
	HandleConnection(conn net.Conn) (net.Conn, error)
}
