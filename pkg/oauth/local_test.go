package oauth

import (
	"crypto/x509"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// generateSelfSignedCert
// ---------------------------------------------------------------------------

func TestGenerateSelfSignedCert(t *testing.T) {
	cert, err := generateSelfSignedCert()
	require.NoError(t, err, "generateSelfSignedCert should not return an error")

	// The Certificate field should contain exactly one DER-encoded cert.
	require.Len(t, cert.Certificate, 1, "should contain exactly one certificate")

	// Parse the DER bytes so we can inspect x509 fields.
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err, "certificate DER should be parseable")

	// PrivateKey must be set.
	assert.NotNil(t, cert.PrivateKey, "PrivateKey must not be nil")

	t.Run("has localhost DNS name", func(t *testing.T) {
		assert.Contains(t, parsed.DNSNames, "localhost",
			"certificate should include 'localhost' in DNSNames")
	})

	t.Run("subject organization", func(t *testing.T) {
		require.NotEmpty(t, parsed.Subject.Organization)
		assert.Equal(t, "Slack MCP Server Local OAuth", parsed.Subject.Organization[0])
	})

	t.Run("validity window", func(t *testing.T) {
		now := time.Now()
		assert.False(t, parsed.NotBefore.After(now),
			"NotBefore should be at or before the current time")
		assert.True(t, parsed.NotAfter.After(now),
			"NotAfter should be in the future")

		// The implementation sets a 1-hour validity window.
		window := parsed.NotAfter.Sub(parsed.NotBefore)
		assert.InDelta(t, time.Hour.Seconds(), window.Seconds(), 5,
			"validity window should be approximately 1 hour")
	})

	t.Run("key usage includes digital signature", func(t *testing.T) {
		assert.True(t, parsed.KeyUsage&x509.KeyUsageDigitalSignature != 0,
			"KeyUsage should include DigitalSignature")
	})

	t.Run("ext key usage includes server auth", func(t *testing.T) {
		require.NotEmpty(t, parsed.ExtKeyUsage, "ExtKeyUsage should not be empty")
		assert.Contains(t, parsed.ExtKeyUsage, x509.ExtKeyUsageServerAuth,
			"ExtKeyUsage should include ServerAuth")
	})

	t.Run("basic constraints valid", func(t *testing.T) {
		assert.True(t, parsed.BasicConstraintsValid,
			"BasicConstraintsValid should be true")
	})

	t.Run("serial number is set", func(t *testing.T) {
		assert.NotNil(t, parsed.SerialNumber, "serial number should be set")
		assert.True(t, parsed.SerialNumber.Sign() > 0,
			"serial number should be a positive integer")
	})

	t.Run("is self-signed", func(t *testing.T) {
		assert.Equal(t, parsed.Issuer.String(), parsed.Subject.String(),
			"certificate should be self-signed (issuer == subject)")
	})
}

func TestGenerateSelfSignedCert_Uniqueness(t *testing.T) {
	cert1, err := generateSelfSignedCert()
	require.NoError(t, err)

	cert2, err := generateSelfSignedCert()
	require.NoError(t, err)

	parsed1, err := x509.ParseCertificate(cert1.Certificate[0])
	require.NoError(t, err)
	parsed2, err := x509.ParseCertificate(cert2.Certificate[0])
	require.NoError(t, err)

	assert.NotEqual(t, parsed1.SerialNumber, parsed2.SerialNumber,
		"two generated certificates should have different serial numbers")
}

// ---------------------------------------------------------------------------
// buildAuthorizeURL
// ---------------------------------------------------------------------------

func TestBuildAuthorizeURL(t *testing.T) {
	clientID := "test-client-id-123"
	state := "random-state-abc"

	rawURL := buildAuthorizeURL(clientID, state)

	parsed, err := url.Parse(rawURL)
	require.NoError(t, err, "buildAuthorizeURL should return a valid URL")

	t.Run("scheme and host", func(t *testing.T) {
		assert.Equal(t, "https", parsed.Scheme)
		// The host comes from edge.GetSlackBaseDomain(), which defaults to
		// "slack.com" (unless SLACK_MCP_GOVSLACK=true).
		assert.Contains(t, parsed.Host, "slack")
	})

	t.Run("path", func(t *testing.T) {
		assert.Equal(t, "/oauth/v2/authorize", parsed.Path)
	})

	params := parsed.Query()

	t.Run("client_id", func(t *testing.T) {
		assert.Equal(t, clientID, params.Get("client_id"))
	})

	t.Run("state", func(t *testing.T) {
		assert.Equal(t, state, params.Get("state"))
	})

	t.Run("redirect_uri", func(t *testing.T) {
		assert.Equal(t, localRedirectURI, params.Get("redirect_uri"))
	})

	t.Run("user_scope contains all expected scopes", func(t *testing.T) {
		expectedScopes := []string{
			"channels:history",
			"channels:read",
			"groups:history",
			"groups:read",
			"im:history",
			"im:read",
			"im:write",
			"mpim:history",
			"mpim:read",
			"mpim:write",
			"users:read",
			"chat:write",
			"search:read",
		}

		scopeStr := params.Get("user_scope")
		require.NotEmpty(t, scopeStr, "user_scope parameter should not be empty")

		scopes := strings.Split(scopeStr, ",")
		for _, expected := range expectedScopes {
			assert.Contains(t, scopes, expected,
				"user_scope should include %q", expected)
		}

		// Verify no extra scopes crept in.
		assert.Len(t, scopes, len(expectedScopes),
			"user_scope should contain exactly the expected number of scopes")
	})
}

func TestBuildAuthorizeURL_DifferentInputs(t *testing.T) {
	url1 := buildAuthorizeURL("client-a", "state-1")
	url2 := buildAuthorizeURL("client-b", "state-2")

	assert.NotEqual(t, url1, url2,
		"different inputs should produce different URLs")
	assert.Contains(t, url1, "client-a")
	assert.Contains(t, url2, "client-b")
	assert.Contains(t, url1, "state-1")
	assert.Contains(t, url2, "state-2")
}

func TestBuildAuthorizeURL_SpecialCharsInClientID(t *testing.T) {
	// Client IDs with dots/dashes are common in Slack apps.
	clientID := "12345.67890"
	state := "abc-def"

	rawURL := buildAuthorizeURL(clientID, state)
	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)

	// url.Values.Encode() should properly encode the client_id.
	assert.Equal(t, clientID, parsed.Query().Get("client_id"))
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

func TestConstants(t *testing.T) {
	assert.Equal(t, "8443", localPort,
		"localPort should be 8443")
	assert.Equal(t, "https://localhost:8443/callback", localRedirectURI,
		"localRedirectURI should point to the local HTTPS callback")
	assert.Equal(t, 5*time.Minute, flowTimeout,
		"flowTimeout should be 5 minutes")
}

// ---------------------------------------------------------------------------
// exchangeCodeForToken
// ---------------------------------------------------------------------------
//
// NOTE: exchangeCodeForToken cannot be easily unit-tested because it POSTs
// to a hardcoded URL derived from edge.GetSlackBaseDomain() (e.g.
// https://slack.com/api/oauth.v2.access). There is no mechanism to inject
// a test server URL without refactoring the production code.
//
// To properly unit-test this function, the Slack base URL would need to be
// made injectable (e.g. via a parameter, package-level variable, or
// interface). Until then, manual / integration testing against the real
// Slack API is necessary.
//
// The function's behaviour can be summarised as:
//   1. POSTs client_id, client_secret, code, and redirect_uri to Slack.
//   2. Decodes the JSON response into an internal struct.
//   3. Returns an error if ok == false.
//   4. Maps authed_user.access_token -> AccessToken, access_token -> BotToken,
//      authed_user.id -> UserID, team.id -> TeamID, bot_user_id -> BotUserID,
//      and sets ExpiresAt to ~1 year from now.

// ---------------------------------------------------------------------------
// openBrowser
// ---------------------------------------------------------------------------
//
// openBrowser is also difficult to unit test meaningfully because it shells
// out to a platform-specific command (open, xdg-open, rundll32). Verifying
// the correct command is chosen for the current GOOS would require either
// mocking exec.Command or running under each target OS.
