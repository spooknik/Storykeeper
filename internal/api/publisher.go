package api

// EventPublisher fans server events out to a user's connected clients.
// internal/events.Hub implements it; handlers must tolerate a nil publisher.
type EventPublisher interface {
	// Publish sends a named event with a JSON-serialisable payload to every
	// session of one user.
	Publish(userID int64, name string, payload any)
	// Broadcast sends a named event to every connected session.
	Broadcast(name string, payload any)
}

func (s *Server) publish(userID int64, name string, payload any) {
	if s.Events != nil {
		s.Events.Publish(userID, name, payload)
	}
}
