package oauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"go.uber.org/zap"
)

const (
	localPort        = "8443"
	localRedirectURI = "https://localhost:8443/callback"
	flowTimeout      = 5 * time.Minute
)

// LocalOAuthFlow runs a local OAuth flow by starting a temporary HTTPS server
// with a self-signed certificate, opening the user's browser to the Slack
// authorize URL, and waiting for the callback with the authorization code.
func LocalOAuthFlow(clientID, clientSecret string, logger *zap.Logger) (*TokenResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), flowTimeout)
	defer cancel()

	// Generate random state for CSRF protection
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}
	state := hex.EncodeToString(stateBytes)

	// Generate self-signed TLS certificate
	tlsCert, err := generateSelfSignedCert()
	if err != nil {
		return nil, fmt.Errorf("failed to generate TLS certificate: %w", err)
	}

	// Build the authorize URL
	authURL := buildAuthorizeURL(clientID, state)

	// Channel to receive the token result from the callback handler
	type result struct {
		token *TokenResponse
		err   error
	}
	resultCh := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		callbackState := r.URL.Query().Get("state")

		if callbackState != state {
			logger.Error("state mismatch in OAuth callback")
			http.Error(w, "Invalid state parameter", http.StatusBadRequest)
			resultCh <- result{err: fmt.Errorf("state mismatch: expected %s, got %s", state, callbackState)}
			return
		}

		if code == "" {
			errMsg := r.URL.Query().Get("error")
			logger.Error("no code in OAuth callback", zap.String("error", errMsg))
			http.Error(w, "No authorization code received", http.StatusBadRequest)
			resultCh <- result{err: fmt.Errorf("no authorization code received: %s", errMsg)}
			return
		}

		logger.Info("received OAuth callback, exchanging code for token")

		token, err := exchangeCodeForToken(clientID, clientSecret, code)
		if err != nil {
			logger.Error("failed to exchange code for token", zap.Error(err))
			http.Error(w, "Failed to exchange authorization code", http.StatusInternalServerError)
			resultCh <- result{err: err}
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<!DOCTYPE html>
<html><head><title>Authorization Successful</title></head>
<body style="font-family:sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0">
<div style="text-align:center">
<h1>Authorization Successful</h1>
<p>You can close this window and return to your terminal.</p>
</div></body></html>`)

		resultCh <- result{token: token}
	})

	server := &http.Server{
		Addr:    ":" + localPort,
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
		},
	}

	// Start the HTTPS server
	serverErrCh := make(chan error, 1)
	go func() {
		logger.Info("starting local HTTPS server", zap.String("address", "https://localhost:"+localPort))
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			serverErrCh <- err
		}
	}()

	// Brief pause to let the server start
	time.Sleep(100 * time.Millisecond)

	// Open the browser
	logger.Info("opening browser for Slack authorization", zap.String("url", authURL))
	if err := openBrowser(authURL); err != nil {
		logger.Warn("failed to open browser automatically", zap.Error(err))
		logger.Info("please open the following URL in your browser", zap.String("url", authURL))
	}

	// Wait for the result, context cancellation, or server error
	var res result
	select {
	case res = <-resultCh:
	case err := <-serverErrCh:
		return nil, fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		res = result{err: fmt.Errorf("OAuth flow timed out after %s", flowTimeout)}
	}

	// Shut down the server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("error shutting down server", zap.Error(err))
	}
	logger.Info("local HTTPS server stopped")

	return res.token, res.err
}

func buildAuthorizeURL(clientID, state string) string {
	userScopes := []string{
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

	params := url.Values{
		"client_id":    {clientID},
		"user_scope":   {strings.Join(userScopes, ",")},
		"redirect_uri": {localRedirectURI},
		"state":        {state},
	}

	return "https://" + edge.GetSlackBaseDomain() + "/oauth/v2/authorize?" + params.Encode()
}

func exchangeCodeForToken(clientID, clientSecret, code string) (*TokenResponse, error) {
	data := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {localRedirectURI},
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.PostForm("https://"+edge.GetSlackBaseDomain()+"/api/oauth.v2.access", data)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		AccessToken string `json:"access_token"`
		AuthedUser  struct {
			ID          string `json:"id"`
			AccessToken string `json:"access_token"`
		} `json:"authed_user"`
		BotUserID string `json:"bot_user_id"`
		Team      struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if !result.OK {
		return nil, fmt.Errorf("slack error: %s", result.Error)
	}

	return &TokenResponse{
		AccessToken: result.AuthedUser.AccessToken,
		BotToken:    result.AccessToken,
		UserID:      result.AuthedUser.ID,
		TeamID:      result.Team.ID,
		BotUserID:   result.BotUserID,
		ExpiresAt:   time.Now().Add(365 * 24 * time.Hour),
	}, nil
}

func generateSelfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to generate private key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Slack MCP Server Local OAuth"},
		},
		DNSNames:              []string{"localhost"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(1 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to create certificate: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}
