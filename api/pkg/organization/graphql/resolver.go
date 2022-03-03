package graphql

import (
	"context"
	"database/sql"
	"errors"
	"getsturdy.com/api/pkg/events"
	"go.uber.org/zap"
	"strings"

	"github.com/gosimple/slug"
	"github.com/graph-gophers/graphql-go"

	"getsturdy.com/api/pkg/auth"
	service_auth "getsturdy.com/api/pkg/auth/service"
	"getsturdy.com/api/pkg/codebase"
	service_codebase "getsturdy.com/api/pkg/codebase/service"
	gqlerrors "getsturdy.com/api/pkg/graphql/errors"
	"getsturdy.com/api/pkg/graphql/resolvers"
	"getsturdy.com/api/pkg/organization"
	service_organization "getsturdy.com/api/pkg/organization/service"
	"getsturdy.com/api/pkg/users"
	service_user "getsturdy.com/api/pkg/users/service"
)

type organizationRootResolver struct {
	service         *service_organization.Service
	authService     *service_auth.Service
	userService     service_user.Service
	codebaseService *service_codebase.Service

	authorRootResolver    resolvers.AuthorRootResolver
	licensesRootResolver  resolvers.LicenseRootResolver
	codebasesRootResolver resolvers.CodebaseRootResolver

	viewEvents   events.EventReader
	eventsSender events.EventSender
	logger       *zap.Logger
}

func New(
	service *service_organization.Service,
	authService *service_auth.Service,
	userService service_user.Service,
	codebaseService *service_codebase.Service,

	authorRootResolver resolvers.AuthorRootResolver,
	licensesRootResolver resolvers.LicenseRootResolver,
	codebasesRootResolver resolvers.CodebaseRootResolver,

	viewEvents events.EventReader,
	eventsSender events.EventSender,
	logger *zap.Logger,

) resolvers.OrganizationRootResolver {
	return &organizationRootResolver{
		service:         service,
		authService:     authService,
		userService:     userService,
		codebaseService: codebaseService,

		authorRootResolver:    authorRootResolver,
		licensesRootResolver:  licensesRootResolver,
		codebasesRootResolver: codebasesRootResolver,

		viewEvents:   viewEvents,
		eventsSender: eventsSender,
		logger:       logger.Named("OrganizationRootResolver"),
	}
}

func (r *organizationRootResolver) Organizations(ctx context.Context) ([]resolvers.OrganizationResolver, error) {
	userID, err := auth.UserID(ctx)
	switch {
	case err == nil:
	case errors.Is(err, auth.ErrUnauthenticated):
		return nil, nil
	default:
		return nil, gqlerrors.Error(err)
	}

	// List of organizations that the user is a member of directly
	explicitMemberships, err := r.service.ListByUserID(ctx, userID)
	if err != nil {
		return nil, gqlerrors.Error(err)
	}

	loaded := make(map[string]struct{})

	var res []resolvers.OrganizationResolver

	for _, org := range explicitMemberships {
		loaded[org.ID] = struct{}{}
		res = append(res, &organizationResolver{
			root: r,
			org:  org,
		})
	}

	// List of organizations that the user is an indirect member of (the user is a member of one of the organizations codebases)
	implicitMemberships, err := r.codebaseService.ListOrgsByUser(ctx, userID)
	if err != nil {
		return nil, gqlerrors.Error(err)
	}

	for _, orgID := range implicitMemberships {
		if _, ok := loaded[orgID]; ok {
			continue
		}
		loaded[orgID] = struct{}{}

		org, err := r.service.GetByID(ctx, orgID)
		if err != nil {
			return nil, gqlerrors.Error(err)
		}

		res = append(res, &organizationResolver{
			root: r,
			org:  org,
		})
	}

	return res, nil
}

func (r *organizationRootResolver) Organization(ctx context.Context, args resolvers.OrganizationArgs) (resolvers.OrganizationResolver, error) {
	var org *organization.Organization

	if args.ID != nil {
		var err error
		org, err = r.service.GetByID(ctx, string(*args.ID))
		if err != nil {
			return nil, gqlerrors.Error(err)
		}
	} else if args.ShortID != nil {
		s := string(*args.ShortID)
		if idx := strings.LastIndex(s, "-"); idx >= 0 {
			s = s[idx+1:]
		}
		var err error
		org, err = r.service.GetByShortID(ctx, organization.ShortOrganizationID(s))
		if err != nil {
			return nil, gqlerrors.Error(err)
		}
	}

	if err := r.authService.CanRead(ctx, org); err != nil {
		return nil, gqlerrors.Error(err)
	}

	return &organizationResolver{org: org, root: r}, nil
}

func (r *organizationRootResolver) CreateOrganization(ctx context.Context, args resolvers.CreateOrganizationArgs) (resolvers.OrganizationResolver, error) {
	org, err := r.service.Create(ctx, args.Input.Name)
	if err != nil {
		return nil, gqlerrors.Error(err)
	}

	return &organizationResolver{root: r, org: org}, nil
}

func (r *organizationRootResolver) UpdateOrganization(ctx context.Context, args resolvers.UpdateOrganizationArgs) (resolvers.OrganizationResolver, error) {
	if len(strings.TrimSpace(args.Input.Name)) == 0 {
		return nil, gqlerrors.Error(gqlerrors.ErrBadRequest, "name", "can't be empty")
	}

	org, err := r.service.GetByID(ctx, string(args.Input.ID))
	if err != nil {
		return nil, gqlerrors.Error(err)
	}

	if authErr := r.authService.CanWrite(ctx, org); authErr != nil {
		return nil, gqlerrors.Error(authErr)
	}

	org, err = r.service.Update(ctx, string(args.Input.ID), args.Input.Name)
	if err != nil {
		return nil, gqlerrors.Error(err)
	}
	return &organizationResolver{root: r, org: org}, nil
}

func (r *organizationRootResolver) AddUserToOrganization(ctx context.Context, args resolvers.AddUserToOrganizationArgs) (resolvers.OrganizationResolver, error) {
	org, err := r.service.GetByID(ctx, string(args.Input.OrganizationID))
	if err != nil {
		return nil, gqlerrors.Error(err)
	}

	if err := r.authService.CanWrite(ctx, org); err != nil {
		return nil, gqlerrors.Error(err)
	}

	user, err := r.userService.GetByEmail(ctx, args.Input.Email)
	if err != nil {
		return nil, gqlerrors.Error(err)
	}

	addedByUserID, err := auth.UserID(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := r.service.AddMember(ctx, org.ID, user.ID, addedByUserID); err != nil {
		return nil, gqlerrors.Error(err)
	}

	return &organizationResolver{root: r, org: org}, nil
}

func (r *organizationRootResolver) RemoveUserFromOrganization(ctx context.Context, args resolvers.RemoveUserFromOrganizationArgs) (resolvers.OrganizationResolver, error) {
	org, err := r.service.GetByID(ctx, string(args.Input.OrganizationID))
	if err != nil {
		return nil, gqlerrors.Error(err)
	}

	if err := r.authService.CanWrite(ctx, org); err != nil {
		return nil, gqlerrors.Error(err)
	}

	removedByUserID, err := auth.UserID(ctx)
	if err != nil {
		return nil, gqlerrors.Error(err)
	}

	if err := r.service.RemoveMember(ctx, org.ID, users.ID(args.Input.UserID), removedByUserID); err != nil {
		return nil, gqlerrors.Error(err)
	}

	return &organizationResolver{root: r, org: org}, nil
}

func (r *organizationRootResolver) UpdatedOrganization(ctx context.Context, args resolvers.UpdatedOrganizationArgs) (<-chan resolvers.OrganizationResolver, error) {
	userID, err := auth.UserID(ctx)
	if err != nil {
		return nil, gqlerrors.Error(err)
	}

	c := make(chan resolvers.OrganizationResolver, 100)
	didErrorOut := false

	cancelFunc := r.viewEvents.SubscribeUser(userID, func(et events.EventType, reference string) error {
		if et == events.OrganizationUpdated {
			id := graphql.ID(reference)
			if args.OrganizationID != nil && id != *args.OrganizationID {
				return nil
			}
			resolver, err := r.Organization(ctx, resolvers.OrganizationArgs{ID: &id})
			if err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return errors.New("disconnected")
			case c <- resolver:
				if didErrorOut {
					didErrorOut = false
				}
				return nil
			default:
				r.logger.Error("dropped subscription event",
					zap.Stringer("user_id", userID),
					zap.Stringer("event_type", et),
					zap.Int("channel_size", len(c)),
				)
				didErrorOut = true
				return nil
			}
		}
		return nil
	})

	go func() {
		<-ctx.Done()
		cancelFunc()
		close(c)
	}()

	return c, nil
}

type organizationResolver struct {
	root *organizationRootResolver
	org  *organization.Organization
}

func (r *organizationResolver) ID() graphql.ID {
	return graphql.ID(r.org.ID)
}

func (r *organizationResolver) ShortID() graphql.ID {
	return graphql.ID(slug.Make(r.org.Name) + "-" + string(r.org.ShortID))
}

func (r *organizationResolver) Name() string {
	return r.org.Name
}

func (r *organizationResolver) Members(ctx context.Context) ([]resolvers.AuthorResolver, error) {
	members, err := r.root.service.Members(ctx, r.org.ID)
	if err != nil {
		return nil, gqlerrors.Error(err)
	}

	var res []resolvers.AuthorResolver

	for _, m := range members {
		author, err := r.root.authorRootResolver.Author(ctx, graphql.ID(m.UserID))
		switch {
		case err == nil:
			res = append(res, author)
		case errors.Is(err, sql.ErrNoRows):
			// skip
		case err != nil:
			return nil, gqlerrors.Error(err)
		}
	}

	return res, nil
}

func (r *organizationResolver) Codebases(ctx context.Context) ([]resolvers.CodebaseResolver, error) {
	userID, err := auth.UserID(ctx)
	if err != nil {
		return nil, gqlerrors.Error(err)
	}

	var isMemberOfOrganization bool

	_, err = r.root.service.GetMember(ctx, r.org.ID, userID)
	switch {
	case err == nil:
		isMemberOfOrganization = true
	case errors.Is(err, sql.ErrNoRows):
		isMemberOfOrganization = false
	case err != nil:
		return nil, gqlerrors.Error(err)
	}

	var codebases []*codebase.Codebase

	// List all codebases in the organization
	if isMemberOfOrganization {
		codebases, err = r.root.codebaseService.ListByOrganization(ctx, r.org.ID)
		if err != nil {
			return nil, gqlerrors.Error(err)
		}
	} else {
		// List codebases that the user is a member of
		codebases, err = r.root.codebaseService.ListByOrganizationAndUser(ctx, r.org.ID, userID)
		if err != nil {
			return nil, gqlerrors.Error(err)
		}
	}

	var res []resolvers.CodebaseResolver

	for _, cb := range codebases {
		id := graphql.ID(cb.ID)
		resolver, err := r.root.codebasesRootResolver.Codebase(ctx, resolvers.CodebaseArgs{ID: &id})
		if err != nil {
			return nil, gqlerrors.Error(err)
		}
		res = append(res, resolver)
	}

	return res, nil
}

func (r *organizationResolver) Licenses(ctx context.Context) ([]resolvers.LicenseResolver, error) {
	return r.root.licensesRootResolver.InternalListForOrganizationID(ctx, r.org.ID)
}

func (r *organizationResolver) Writeable(ctx context.Context) bool {
	if err := r.root.authService.CanWrite(ctx, r.org); err == nil {
		return true
	}
	return false
}
