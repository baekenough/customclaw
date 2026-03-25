// Package tokenrefresh proactively refreshes OAuth tokens for the Claude and
// Codex CLIs before they expire, eliminating the need for human re-authentication.
package tokenrefresh

import "encoding/json"

// ---------------------------------------------------------------------------
// Claude credentials
// ---------------------------------------------------------------------------

// claudeCredentials mirrors the on-disk structure of the Claude CLI
// credentials file ($CLAUDE_CONFIG_DIR/.credentials.json).
//
// Fields not modelled here (e.g. mcpOAuth) are captured in Extra so that
// round-tripping the file never silently drops them.
type claudeCredentials struct {
	ClaudeAiOauth claudeOAuthToken       `json:"claudeAiOauth"`
	Extra         map[string]json.RawMessage `json:"-"`
}

// claudeOAuthToken holds the OAuth token fields nested under "claudeAiOauth".
type claudeOAuthToken struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAt        int64    `json:"expiresAt"` // milliseconds since epoch
	Scopes           []string `json:"scopes"`
	SubscriptionType string   `json:"subscriptionType"`
	RateLimitTier    string   `json:"rateLimitTier"`
}

// UnmarshalJSON implements json.Unmarshaler so that unknown top-level keys are
// preserved in Extra for lossless round-tripping.
func (c *claudeCredentials) UnmarshalJSON(data []byte) error {
	// First pass: decode into a raw map to capture all keys.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Decode the known field.
	if v, ok := raw["claudeAiOauth"]; ok {
		if err := json.Unmarshal(v, &c.ClaudeAiOauth); err != nil {
			return err
		}
	}

	// Save everything else verbatim.
	c.Extra = make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		if k == "claudeAiOauth" {
			continue
		}
		c.Extra[k] = v
	}
	return nil
}

// MarshalJSON implements json.Marshaler so that Extra fields are serialised
// back alongside the known claudeAiOauth field.
func (c claudeCredentials) MarshalJSON() ([]byte, error) {
	// Build the output map from Extra, then overlay the known field.
	out := make(map[string]json.RawMessage, len(c.Extra)+1)
	for k, v := range c.Extra {
		out[k] = v
	}

	v, err := json.Marshal(c.ClaudeAiOauth)
	if err != nil {
		return nil, err
	}
	out["claudeAiOauth"] = json.RawMessage(v)
	return json.Marshal(out)
}

// claudeTokenResponse is the expected response body from the Claude OAuth
// token endpoint on a successful refresh_token grant.
type claudeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"` // may be absent; keep existing if so
	ExpiresIn    int64  `json:"expires_in"`    // seconds
	TokenType    string `json:"token_type"`
}

// ---------------------------------------------------------------------------
// Codex credentials
// ---------------------------------------------------------------------------

// codexCredentials mirrors the on-disk structure of the Codex CLI auth file
// ($CODEX_CONFIG_DIR/auth.json).
//
// Fields not modelled here are preserved in Extra for lossless round-tripping.
type codexCredentials struct {
	AuthMode    string            `json:"auth_mode"`
	OpenAIKey   *string           `json:"OPENAI_API_KEY"` // null when using OAuth
	Tokens      codexTokens       `json:"tokens"`
	LastRefresh string            `json:"last_refresh"` // RFC3339Nano
	Extra       map[string]json.RawMessage `json:"-"`
}

// codexTokens holds the nested token fields inside codexCredentials.
type codexTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

// UnmarshalJSON implements json.Unmarshaler, preserving unknown keys in Extra.
func (c *codexCredentials) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	knownFields := map[string]bool{
		"auth_mode":      true,
		"OPENAI_API_KEY": true,
		"tokens":         true,
		"last_refresh":   true,
	}

	if v, ok := raw["auth_mode"]; ok {
		if err := json.Unmarshal(v, &c.AuthMode); err != nil {
			return err
		}
	}
	if v, ok := raw["OPENAI_API_KEY"]; ok {
		if err := json.Unmarshal(v, &c.OpenAIKey); err != nil {
			return err
		}
	}
	if v, ok := raw["tokens"]; ok {
		if err := json.Unmarshal(v, &c.Tokens); err != nil {
			return err
		}
	}
	if v, ok := raw["last_refresh"]; ok {
		if err := json.Unmarshal(v, &c.LastRefresh); err != nil {
			return err
		}
	}

	c.Extra = make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		if knownFields[k] {
			continue
		}
		c.Extra[k] = v
	}
	return nil
}

// MarshalJSON implements json.Marshaler, merging Extra back into the output.
func (c codexCredentials) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(c.Extra)+4)
	for k, v := range c.Extra {
		out[k] = v
	}

	encode := func(key string, val any) error {
		b, err := json.Marshal(val)
		if err != nil {
			return err
		}
		out[key] = json.RawMessage(b)
		return nil
	}

	if err := encode("auth_mode", c.AuthMode); err != nil {
		return nil, err
	}
	if err := encode("OPENAI_API_KEY", c.OpenAIKey); err != nil {
		return nil, err
	}
	if err := encode("tokens", c.Tokens); err != nil {
		return nil, err
	}
	if err := encode("last_refresh", c.LastRefresh); err != nil {
		return nil, err
	}

	return json.Marshal(out)
}

// codexTokenResponse is the expected response body from the Auth0 token
// endpoint on a successful refresh_token grant.
type codexTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"` // may be absent; keep existing if so
	IDToken      string `json:"id_token"`      // may be absent
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"` // seconds
}
