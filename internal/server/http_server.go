package server

import (
	"fmt"
	"net/http"

	"github.com/IsorilovA/pauza-server/internal/config"
)

func newHTTPServer(cfg *config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: handler,
	}
}
