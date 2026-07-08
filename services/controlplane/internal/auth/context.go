package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxOrgID
	ctxClusterID
	ctxToken
)

// TokenPrincipal is the authenticated identity behind an API bearer token: it is
// bound to exactly one org and carries the token's effective role. It is set
// ALONGSIDE ctxUser (the token's creating user, used for actor attribution/FKs).
// Its presence signals authz code to scope strictly to OrgID and to refuse
// platform-admin + token-management operations.
type TokenPrincipal struct {
	TokenID uuid.UUID
	OrgID   uuid.UUID
	Role    string
}

// WithTokenPrincipal attaches an API-token principal to the context.
func WithTokenPrincipal(ctx context.Context, tp *TokenPrincipal) context.Context {
	return context.WithValue(ctx, ctxToken, tp)
}

// TokenFrom returns the API-token principal, if the request was token-authed.
func TokenFrom(ctx context.Context) (*TokenPrincipal, bool) {
	tp, ok := ctx.Value(ctxToken).(*TokenPrincipal)
	return tp, ok && tp != nil
}

// UserFrom returns the authenticated user attached by the session middleware.
func UserFrom(ctx context.Context) (*model.User, bool) {
	u, ok := ctx.Value(ctxUser).(*model.User)
	return u, ok
}

// OrgIDFrom returns the current org id from the session.
func OrgIDFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxOrgID).(uuid.UUID)
	return id, ok
}

// ClusterIDFrom returns the cluster id attached by the agent middleware.
func ClusterIDFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxClusterID).(uuid.UUID)
	return id, ok
}
