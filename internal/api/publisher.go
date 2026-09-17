package api

// EventPublisher fans server events out to a user's connected clients.
// internal/events.Hub implements it; handlers must tolerate a nil publisher.
type EventPublisher interface {
	// Publish sends a named event with a JSON-serialisable payload to every
	// session of one user.
	Publish(userID int64, name string, payload any)
	// Broadcast sends a named event to every connected session.
	Broadcast(name string, payload any)
	// CloseSession terminates the live event streams of one session, so a
	// revoked session stops receiving events at once.
	CloseSession(sessionID int64)
	// CloseUser terminates the live event streams of every session belonging
	// to one user (password change, account deletion).
	CloseUser(userID int64)
}

func (s *Server) publish(userID int64, name string, payload any) {
	if s.Events != nil {
		s.Events.Publish(userID, name, payload)
	}
}

// closeSession disconnects a revoked session's event streams, if a publisher
// is configured.
func (s *Server) closeSession(sessionID int64) {
	if s.Events != nil {
		s.Events.CloseSession(sessionID)
	}
}

// closeUser disconnects every event stream of a user whose sessions have all
// been invalidated, if a publisher is configured.
func (s *Server) closeUser(userID int64) {
	if s.Events != nil {
		s.Events.CloseUser(userID)
	}
}
