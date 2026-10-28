package session

// UserNote is an exact queued human message. SessionID names its original raw
// archive for acknowledgement; neither this envelope nor restore grants tools.
type UserNote struct{ ID, Text, SessionID string }
