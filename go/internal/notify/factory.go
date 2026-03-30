package notify

// New creates the appropriate Notifier based on available credentials.
// If token is non-empty, returns a SlackNotifier; otherwise LogNotifier.
func New(token, defaultChannel string) Notifier {
	if token != "" {
		return NewSlackNotifier(token, defaultChannel)
	}
	return NewLogNotifier()
}
