package auth

type contextKey string

const (
	// RedmineKeyContextKey is the key used to store/retrieve the Redmine API Key from context
	RedmineKeyContextKey contextKey = "redmine_api_key"
)
