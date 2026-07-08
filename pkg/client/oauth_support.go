package client

import (
	"errors"
	"strings"

	"github.com/dynatrace-oss/dtctl/pkg/auth"
	"github.com/dynatrace-oss/dtctl/pkg/config"
)

// GetTokenWithOAuthSupport retrieves a token from config with OAuth token refresh support,
// using the current context's environment to detect the OAuth configuration.
func GetTokenWithOAuthSupport(cfg *config.Config, tokenRef string) (string, error) {
	var environmentURL string
	if ctx, err := cfg.CurrentContextObj(); err == nil {
		environmentURL = ctx.Environment
	}
	return GetTokenForContext(cfg, environmentURL, tokenRef)
}

// GetTokenForContext retrieves a token from config with OAuth token refresh support,
// detecting the OAuth configuration from the supplied environment URL. Use this when the
// token may belong to a context other than the current one (e.g. `dtctl ctx token <name>`),
// since the OAuth environment determines both the refresh endpoint and the storage key.
func GetTokenForContext(cfg *config.Config, environmentURL, tokenRef string) (string, error) {
	// First, try to get it as an OAuth token (via keyring or file-based storage)
	if config.IsOAuthStorageAvailable() && environmentURL != "" {
		// Detect environment from the context's URL
		oauthConfig := auth.OAuthConfigFromEnvironmentURL(environmentURL)
		tokenManager, err := auth.NewTokenManager(oauthConfig)
		if err != nil {
			return "", err
		}

		// Try to get as OAuth token (will auto-refresh if needed)
		token, err := tokenManager.GetToken(tokenRef)
		if err == nil {
			return token, nil
		}

		// Fall back to regular API token lookup when:
		//   - the OAuth entry does not exist, or
		//   - the cached OAuth session was revoked server-side (invalid_grant);
		//     the auth layer has already evicted the stale cache entry.
		if !isOAuthTokenNotFoundError(err) && !errors.Is(err, auth.ErrOAuthSessionRevoked) {
			return "", err
		}
	}

	// Fall back to regular token lookup
	return cfg.GetToken(tokenRef)
}

// RefreshedTokenForContext re-resolves the context's bearer token after a
// request was rejected with HTTP 401. GetTokenForContext already refreshes
// tokens that are near expiry locally; when the cached token still looks
// valid but the server rejected it anyway (clock skew, stale ExpiresAt in
// compact keyring storage), the OAuth refresh is forced so the retry never
// re-sends the token the server just bounced. Static API tokens come back
// unchanged — the caller sees rejected == fresh and gives up.
func RefreshedTokenForContext(cfg *config.Config, environmentURL, tokenRef, rejected string) (string, error) {
	token, err := GetTokenForContext(cfg, environmentURL, tokenRef)
	if err != nil || token != rejected {
		return token, err
	}
	if !config.IsOAuthStorageAvailable() || environmentURL == "" {
		return token, nil
	}
	tokenManager, err := auth.NewTokenManager(auth.OAuthConfigFromEnvironmentURL(environmentURL))
	if err != nil {
		return "", err
	}
	refreshed, err := tokenManager.RefreshToken(tokenRef)
	if err != nil {
		// Not an OAuth entry (plain API token) or the session is revoked —
		// nothing fresher exists; the caller surfaces the original 401.
		return token, nil
	}
	return refreshed.AccessToken, nil
}

// NewFromConfigWithOAuth creates a new client from config with OAuth support.
//
// Deprecated: Use NewFromConfig instead, which now supports OAuth tokens automatically.
func NewFromConfigWithOAuth(cfg *config.Config) (*Client, error) {
	return NewFromConfig(cfg)
}

func isOAuthTokenNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "not found in keyring") ||
		strings.Contains(errMsg, "not found in file store") ||
		strings.Contains(errMsg, "token") && strings.Contains(errMsg, "not found")
}
