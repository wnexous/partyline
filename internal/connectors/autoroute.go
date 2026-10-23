package connectors

import (
	"github.com/wnexous/partyline/internal/api/dto"
)

// Status details the retired catch-all routing set on a connector (ROUTE-03, ROUTE-08). Nothing
// sets them any more, but a connector may still carry one until its settings are saved (ROUTE-09)
// and rooms still clear the first when a catch-all room is deleted.
const (
	AutoRouteUnavailable = dto.AutoRouteUnavailable
	AutoRouteLimited     = dto.AutoRouteLimited
)

// IsAutoRouteDetail tells whether a status detail was set by catch-all routing.
func IsAutoRouteDetail(detail string) bool {
	return detail == AutoRouteUnavailable || detail == AutoRouteLimited
}
