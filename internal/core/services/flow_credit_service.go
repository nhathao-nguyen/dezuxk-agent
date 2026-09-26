package services

import (
	"context"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type FlowCreditService struct {
	sessionRepo ports.SessionRepository
	flowClient  ports.FlowClient
	metrics     *domain.ContractMetrics
}

func NewFlowCreditService(sr ports.SessionRepository, fc ports.FlowClient, metrics *domain.ContractMetrics) ports.FlowCreditUseCase {
	return &FlowCreditService{
		sessionRepo: sr,
		flowClient:  fc,
		metrics:     metrics,
	}
}

func (s *FlowCreditService) GetCredits(ctx context.Context) (string, domain.FlowCreditBalance, error) {
	account, err := s.sessionRepo.GetAvailable(ctx, domain.ServiceFlow, 0)
	if err != nil {
		return "", domain.FlowCreditBalance{}, err
	}

	var callErr error
	defer s.sessionRepo.Release(account, callErr)

	var balance domain.FlowCreditBalance
	callErr = session.RetryAfterRefresh(ctx, s.sessionRepo, account, domain.ServiceFlow, func() error {
		var inner error
		balance, inner = s.flowClient.GetCreditsBalance(ctx, account)
		return inner
	})
	if callErr != nil {
		return "", domain.FlowCreditBalance{}, callErr
	}
	return account.ID, balance, nil
}
