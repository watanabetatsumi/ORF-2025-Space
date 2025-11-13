package main

import (
	"github.com/gin-gonic/gin"

	"github.com/watanabetatsumi/backend-server/intenal/service"
)

func main() {
	r := gin.Default()

	bpsrv := service.NewBackendService()

	r.Run()
}
