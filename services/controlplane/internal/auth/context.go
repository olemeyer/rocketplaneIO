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
)

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
