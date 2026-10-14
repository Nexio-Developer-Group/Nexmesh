package preview

import (
	"context"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

// Share links arrive in a follow-up change; until then no link is ever valid.

func (s *Service) shareFromSecret(context.Context, string, int, string) (*domain.ShareLink, bool) {
	return nil, false
}
func (s *Service) noteShareHit(context.Context, string, string)          {}
func (s *Service) shareActive(context.Context, string, string, int) bool { return false }
