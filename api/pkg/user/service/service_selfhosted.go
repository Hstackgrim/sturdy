//go:build enterprise || !cloud
// +build enterprise !cloud

package service

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pkg/errors"
	"go.uber.org/zap"

	"getsturdy.com/api/pkg/analytics"
	"getsturdy.com/api/pkg/emails/transactional"
	service_jwt "getsturdy.com/api/pkg/jwt/service"
	service_onetime "getsturdy.com/api/pkg/onetime/service"
	service_organization "getsturdy.com/api/pkg/organization/service"
	"getsturdy.com/api/pkg/user"
	db_user "getsturdy.com/api/pkg/user/db"
)

type Service struct {
	*commonService

	organizationService *service_organization.Service
}

func New(
	logger *zap.Logger,
	userRepo db_user.Repository,
	jwtService *service_jwt.Service,
	onetimeService *service_onetime.Service,
	transactionalEmailSender transactional.EmailSender,
	analyticsClient analytics.Client,

	organizationService *service_organization.Service,
) *Service {
	return &Service{
		commonService: &commonService{
			logger:                   logger,
			userRepo:                 userRepo,
			jwtService:               jwtService,
			onetimeService:           onetimeService,
			transactionalEmailSender: transactionalEmailSender,
			analyticsClient:          analyticsClient,
		},

		organizationService: organizationService,
	}
}

func (s *Service) CreateWithPassword(ctx context.Context, name, password, email string) (*user.User, error) {
	usr, err := s.commonService.CreateWithPassword(ctx, name, password, email)
	if err != nil {
		return nil, err
	}

	// If this instance has an organization, auto-add this user
	first, err := s.organizationService.GetFirst(ctx)
	switch {
	case err == nil:
		// add this user
		if _, err := s.organizationService.AddMember(ctx, first.ID, usr.ID, usr.ID); err != nil {
			return nil, fmt.Errorf("failed to add member to existing org: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
	// first org has not been created yet, this user will create it later
	case err != nil:
		return nil, fmt.Errorf("failed to check if an organization already exists: %w", err)
	}

	return usr, nil
}
