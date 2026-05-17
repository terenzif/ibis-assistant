package auth

type contextKey string

type TokenProvider interface {
	GetGitToken(originUrl string) string
}

const (
	// RedmineKeyContextKey is the key used to store/retrieve the Redmine API Key from context
	RedmineKeyContextKey contextKey = "redmine_api_key"
	// TokenProviderKey is the key used to store/retrieve the token provider from context
	TokenProviderKey contextKey = "token_provider"
)
