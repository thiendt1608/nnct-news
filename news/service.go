package news

import (
	"context"
	"time"

	_ "time/tzdata" // container may lack zoneinfo; digest_date uses Asia/Ho_Chi_Minh
)

// Service is the single Encore service for nnct-news.
//
//encore:service
type Service struct{}

func initService() (*Service, error) { return &Service{}, nil }

// Ping is a placeholder health endpoint for the Phase 0 skeleton.
//
//encore:api private
func (s *Service) Ping(ctx context.Context) (*PingResponse, error) {
	return &PingResponse{Now: time.Now().UTC()}, nil
}

type PingResponse struct {
	Now time.Time `json:"now"`
}
