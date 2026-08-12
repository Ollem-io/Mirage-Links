package domain

import "time"

type LinkStatus string

const (
	StatusCreating LinkStatus = "creating"
	StatusStarting LinkStatus = "starting"
	StatusHealthy  LinkStatus = "healthy"
	StatusActive   LinkStatus = "active"
	StatusStopping LinkStatus = "stopping"
	StatusDeleted  LinkStatus = "deleted"
	StatusExpired  LinkStatus = "expired"
	StatusFailed   LinkStatus = "failed"
)

// Terminal means permanently terminal. Failed is deliberately restartable: a
// manual or configured automatic restart goes through Starting and must health
// gate back through Healthy before Active.
func (s LinkStatus) Terminal() bool { return s == StatusDeleted || s == StatusExpired }
func (s LinkStatus) CanTransition(to LinkStatus) bool {
	if s == to {
		return true
	}
	if s.Terminal() {
		return false
	}
	switch s {
	case StatusCreating:
		return to == StatusStarting || to == StatusFailed || to == StatusDeleted || to == StatusExpired
	case StatusStarting:
		return to == StatusHealthy || to == StatusFailed || to == StatusStopping || to == StatusExpired
	case StatusHealthy:
		return to == StatusActive || to == StatusFailed || to == StatusStopping || to == StatusExpired
	case StatusActive:
		return to == StatusFailed || to == StatusStopping || to == StatusExpired
	case StatusStopping:
		return to == StatusDeleted || to == StatusFailed || to == StatusExpired
	case StatusFailed:
		return to == StatusStarting || to == StatusStopping || to == StatusDeleted || to == StatusExpired
	}
	return false
}

// Link holds both desired execution state and lifecycle recovery metadata.
// Credentials never belong here: only Space has the persisted token hash.
type Link struct {
	ID              LinkID        `json:"id"`
	SpaceID         SpaceID       `json:"space_id"`
	Name            LinkName      `json:"name"`
	Status          LinkStatus    `json:"status"`
	Command         string        `json:"-"`
	Folder          string        `json:"-"`
	HealthCheck     HealthCheck   `json:"health_check"`
	Grace           time.Duration `json:"grace"`
	ExpiresAt       time.Time     `json:"expires_at"`
	AllocatedPort   int           `json:"allocated_port,omitempty"`
	ProcessIdentity string        `json:"process_identity,omitempty"`
	AutoRestart     bool          `json:"auto_restart"`
	RestartCount    int           `json:"restart_count"`
	NextRestartAt   time.Time     `json:"next_restart_at,omitempty"`
}

func (l *Link) Transition(to LinkStatus) error {
	if !l.Status.CanTransition(to) {
		return NewConflict("illegal lifecycle transition")
	}
	l.Status = to
	return nil
}
func (l Link) Expired(now time.Time) bool { return Expired(now, l.ExpiresAt) }

type Space struct {
	ID        SpaceID   `json:"id"`
	Alias     Alias     `json:"alias"`
	ExpiresAt time.Time `json:"expires_at"`
	TokenHash TokenHash `json:"-"`
}

func (s Space) Expired(now time.Time) bool { return Expired(now, s.ExpiresAt) }
