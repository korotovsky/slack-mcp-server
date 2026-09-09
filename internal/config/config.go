package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/paymog/slack-cli/internal/credstore"
)

// Config holds resolved Slack credentials and global CLI options. Tokens follow
// the same env-var contract as the upstream MCP server (SLACK_MCP_XOX*); the CLI
// also accepts SLACK_CLI_* aliases on input.
type Config struct {
	XOXP     string
	XOXB     string
	XOXC     string
	XOXD     string
	GovSlack bool

	Profile string
	Raw     bool
	NoCache bool
	Verbose bool
	Timeout time.Duration
}

func FromEnv() Config {
	return Config{Timeout: 2 * time.Minute}
}

// ApplyEnv fills any unset credential field from the environment.
func (c *Config) ApplyEnv() {
	if c.XOXP == "" {
		c.XOXP = firstEnv("SLACK_MCP_XOXP_TOKEN", "SLACK_CLI_XOXP_TOKEN")
	}
	if c.XOXB == "" {
		c.XOXB = firstEnv("SLACK_MCP_XOXB_TOKEN", "SLACK_CLI_XOXB_TOKEN")
	}
	if c.XOXC == "" {
		c.XOXC = firstEnv("SLACK_MCP_XOXC_TOKEN", "SLACK_CLI_XOXC_TOKEN")
	}
	if c.XOXD == "" {
		c.XOXD = firstEnv("SLACK_MCP_XOXD_TOKEN", "SLACK_CLI_XOXD_TOKEN")
	}
	if !c.GovSlack && firstEnv("SLACK_MCP_GOVSLACK", "SLACK_CLI_GOVSLACK") == "true" {
		c.GovSlack = true
	}
}

// Resolve fills credentials following this precedence (highest first):
//
//  1. explicit tokens via flags or SLACK_MCP_*/SLACK_CLI_* env vars
//  2. --profile flag  -> stored profile (keyring bundle + metadata)
//  3. default profile -> stored profile
//
// Explicit tokens combined with --profile is rejected as ambiguous.
func (c *Config) Resolve(store *credstore.Store) error {
	c.ApplyEnv()
	explicit := c.hasAnyToken()

	if explicit && c.Profile != "" {
		return errors.New("cannot combine --profile with explicit tokens (flags or SLACK_MCP_* env); choose one")
	}
	if explicit {
		return nil
	}

	name := c.Profile
	if name == "" {
		name = store.Default
	}
	if name == "" {
		return nil // no credentials yet; RequireAuth reports it when a command needs them
	}

	p, ok := store.Profiles[name]
	if !ok {
		return fmt.Errorf("profile %q not found; run `slack-cli auth login` or `slack-cli auth list`", name)
	}
	tok, err := store.Tokens(name)
	if err != nil {
		return fmt.Errorf("no keyring entry for profile %q; re-run `slack-cli auth login`: %w", name, err)
	}
	c.XOXP, c.XOXB, c.XOXC, c.XOXD = tok.XOXP, tok.XOXB, tok.XOXC, tok.XOXD
	if p.GovSlack {
		c.GovSlack = true
	}
	return nil
}

func (c Config) hasAnyToken() bool {
	return c.XOXP != "" || c.XOXB != "" || c.XOXC != "" || c.XOXD != ""
}

// RequireAuth verifies a usable credential combination is present.
func (c Config) RequireAuth() error {
	if c.XOXP != "" || c.XOXB != "" || (c.XOXC != "" && c.XOXD != "") {
		return nil
	}
	return errors.New("no Slack credentials: set SLACK_MCP_XOXP_TOKEN (or xoxb, or xoxc+xoxd), pass --xoxp/--xoxc/--xoxd, or run `slack-cli auth login`")
}

// HasExplicitEnv reports whether any Slack token is provided via environment
// variables (SLACK_MCP_* or SLACK_CLI_* aliases).
func (c Config) HasExplicitEnv() bool {
	return firstEnv("SLACK_MCP_XOXP_TOKEN", "SLACK_CLI_XOXP_TOKEN") != "" ||
		firstEnv("SLACK_MCP_XOXB_TOKEN", "SLACK_CLI_XOXB_TOKEN") != "" ||
		firstEnv("SLACK_MCP_XOXC_TOKEN", "SLACK_CLI_XOXC_TOKEN") != "" ||
		firstEnv("SLACK_MCP_XOXD_TOKEN", "SLACK_CLI_XOXD_TOKEN") != ""
}

// Mode returns a human label for the active auth mode.
func (c Config) Mode() string {
	switch {
	case c.XOXP != "":
		return "user (xoxp)"
	case c.XOXB != "":
		return "bot (xoxb)"
	case c.XOXC != "" && c.XOXD != "":
		return "session (xoxc/xoxd)"
	default:
		return "none"
	}
}

// ApplyToEnv writes resolved credentials into the SLACK_MCP_* env vars so the
// reused provider.New (which reads env) picks them up. Only non-empty values
// are set, so it never clobbers an unrelated env var.
func (c Config) ApplyToEnv() {
	setIf := func(k, v string) {
		if v != "" {
			_ = os.Setenv(k, v)
		}
	}
	setIf("SLACK_MCP_XOXP_TOKEN", c.XOXP)
	setIf("SLACK_MCP_XOXB_TOKEN", c.XOXB)
	setIf("SLACK_MCP_XOXC_TOKEN", c.XOXC)
	setIf("SLACK_MCP_XOXD_TOKEN", c.XOXD)
	if c.GovSlack {
		_ = os.Setenv("SLACK_MCP_GOVSLACK", "true")
	}
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}
