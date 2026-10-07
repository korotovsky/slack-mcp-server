package transport

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type recordingTransport func(*http.Request) (*http.Response, error)

func TestUnitTLSVerificationConfiguration(t *testing.T) {
	t.Setenv("SLACK_MCP_PROXY", "")
	t.Setenv("SLACK_MCP_SERVER_CA", "")
	t.Setenv("SLACK_MCP_SERVER_CA_TOOLKIT", "")
	for _, customTLS := range []string{"", "true"} {
		for _, tc := range []struct {
			value    string
			insecure bool
		}{
			{"", false}, {"false", false}, {"0", false}, {"FALSE", false},
			{"true", true}, {"1", true}, {"TRUE", true},
		} {
			t.Run("custom="+customTLS+"/insecure="+tc.value, func(t *testing.T) {
				t.Setenv("SLACK_MCP_CUSTOM_TLS", customTLS)
				t.Setenv("SLACK_MCP_SERVER_CA_INSECURE", tc.value)
				client := ProvideHTTPClient(nil, zap.NewNop())
				wrapper := client.Transport.(*UserAgentTransport)
				var got bool
				if customTLS == "" {
					got = wrapper.roundTripper.(*http.Transport).TLSClientConfig.InsecureSkipVerify
				} else {
					got = wrapper.roundTripper.(*uTLSTransport).tlsConfig.InsecureSkipVerify
				}
				if got != tc.insecure {
					t.Fatalf("InsecureSkipVerify=%v, want %v", got, tc.insecure)
				}
			})
		}
	}
}

func TestUnitTLSVerificationRejectsInvalidBoolean(t *testing.T) {
	t.Setenv("SLACK_MCP_PROXY", "")
	t.Setenv("SLACK_MCP_CUSTOM_TLS", "")
	t.Setenv("SLACK_MCP_SERVER_CA", "")
	t.Setenv("SLACK_MCP_SERVER_CA_TOOLKIT", "")
	t.Setenv("SLACK_MCP_SERVER_CA_INSECURE", "typo")
	// Replace the fatal exit with a panic so configuration failure is testable.
	logger := zap.NewNop().WithOptions(zap.WithFatalHook(zapcore.WriteThenPanic))
	defer func() {
		if recover() == nil {
			t.Error("invalid boolean must fail configuration")
		}
	}()
	ProvideHTTPClient(nil, logger)
}

func (f recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestUnitSessionCookieMissingMetadata(t *testing.T) {
	target, err := url.Parse("https://slack.com/api/auth.test")
	if err != nil {
		t.Fatal(err)
	}
	if sessionCookieMatchesURL(nil, target) {
		t.Fatal("nil cookie must not match")
	}
	cookie := &http.Cookie{Name: "d", Value: "dummy-session", Domain: ".slack.com"}
	if sessionCookieMatchesURL(cookie, nil) {
		t.Fatal("nil destination must not match")
	}
	target.Scheme = "http"
	if sessionCookieMatchesURL(cookie, target) {
		t.Fatal("session cookies require HTTPS even without Secure set")
	}
}

func TestUnitSessionCookieDestination(t *testing.T) {
	tests := []struct {
		name, destination, domain string
		wantCookie                bool
	}{
		{"root", "https://slack.com/api/auth.test", ".slack.com", true},
		{"workspace", "https://team.slack.com/api/auth.test", ".slack.com", true},
		{"files", "https://files.slack.com/file", ".slack.com", true},
		{"enterprise", "https://team.enterprise.slack.com/api/auth.test", ".slack.com", true},
		{"case insensitive", "https://TEAM.SLACK.COM/api/auth.test", ".SLACK.COM", true},
		{"port", "https://team.slack.com:443/api/auth.test", "slack.com", true},
		{"unrelated", "https://example.com/file", ".slack.com", false},
		{"suffix deception", "https://slack.com.example.com/file", ".slack.com", false},
		{"missing label boundary", "https://notslack.com/file", ".slack.com", false},
		{"userinfo deception", "https://slack.com@example.com/file", ".slack.com", false},
		{"http", "http://team.slack.com/api/auth.test", ".slack.com", false},
		{"unspecified domain", "https://slack.com/api/auth.test", "", false},
		{"empty domain", "https://slack.com/api/auth.test", ".", false},
		{"IP suffix", "https://127.0.0.1/file", "0.0.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodGet, tt.destination, nil)
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("User-Agent", "original")
			underlying := recordingTransport(func(got *http.Request) (*http.Response, error) {
				cookie, err := got.Cookie("d")
				if tt.wantCookie {
					if err != nil || cookie.Value != "dummy-session" {
						t.Errorf("missing expected session cookie: %v", err)
					}
				} else if err != http.ErrNoCookie {
					t.Errorf("session cookie forwarded to %s", tt.destination)
				}
				if got.UserAgent() != "test-agent" {
					t.Errorf("unexpected user agent: %q", got.UserAgent())
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: got}, nil
			})
			tr := NewUserAgentTransport(underlying, "test-agent", []*http.Cookie{{Name: "d", Value: "dummy-session", Domain: tt.domain, Secure: true}}, zap.NewNop())
			resp, err := tr.RoundTrip(r)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if r.Header.Get("Cookie") != "" || r.UserAgent() != "original" {
				t.Fatal("transport mutated the original request")
			}
		})
	}
}

func TestUnitSessionCookieRedirect(t *testing.T) {
	for _, destination := range []string{"https://files.slack.com/file", "https://example.com/file", "http://team.slack.com/file"} {
		t.Run(destination, func(t *testing.T) {
			calls := 0
			underlying := recordingTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				wantCookie := calls == 1 || destination == "https://files.slack.com/file"
				_, err := r.Cookie("d")
				if (err == nil) != wantCookie {
					t.Errorf("request %d to %s: cookie present=%v, want %v", calls, r.URL, err == nil, wantCookie)
				}
				resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}
				if calls == 1 {
					resp.StatusCode = http.StatusFound
					resp.Header.Set("Location", destination)
				}
				return resp, nil
			})
			client := &http.Client{Transport: NewUserAgentTransport(underlying, "test-agent", []*http.Cookie{{Name: "d", Value: "dummy-session", Domain: ".slack.com", Secure: true}}, zap.NewNop())}
			resp, err := client.Get("https://team.slack.com/file")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if calls != 2 {
				t.Fatalf("got %d requests, want 2", calls)
			}
		})
	}
}
