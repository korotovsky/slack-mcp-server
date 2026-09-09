package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/paymog/slack-cli/internal/cmds"
	"github.com/paymog/slack-cli/internal/config"
	"github.com/paymog/slack-cli/internal/credstore"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	cfg := config.FromEnv()
	var cancelTimeout context.CancelFunc

	root := &cobra.Command{
		Use:           "slack-cli",
		Short:         "Slack workspace tools as a CLI — no daemon, no MCP server process",
		Long:          "slack-cli exposes the slack-mcp-server toolset as ordinary subcommands.\nEach invocation is a short-lived process that reads the shared on-disk cache,\nso running many agents no longer means one resident MCP server per agent.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Apply the request timeout to the command context (cancelled in
			// PersistentPostRun). A short-lived CLI tolerates the rare leak on
			// the error path, where the process exits immediately anyway.
			if cfg.Timeout > 0 {
				ctx, cancel := context.WithTimeout(cmd.Context(), cfg.Timeout)
				cmd.SetContext(ctx)
				cancelTimeout = cancel
			}
			// Credential-management commands (auth login/list/...) run before
			// resolution; everything else resolves the active profile/env.
			if cmd.Annotations[skipResolveAnnotation] == "true" {
				cfg.ApplyEnv()
				return nil
			}
			store, err := credstore.Load()
			if err != nil {
				return err
			}
			return cfg.Resolve(store)
		},
		PersistentPostRun: func(_ *cobra.Command, _ []string) {
			if cancelTimeout != nil {
				cancelTimeout()
			}
		},
	}

	f := root.PersistentFlags()
	f.StringVar(&cfg.XOXP, "xoxp", "", "Slack user OAuth token (xoxp-...). Env: SLACK_MCP_XOXP_TOKEN")
	f.StringVar(&cfg.XOXB, "xoxb", "", "Slack bot token (xoxb-...). Env: SLACK_MCP_XOXB_TOKEN")
	f.StringVar(&cfg.XOXC, "xoxc", "", "Slack browser token (xoxc-...). Env: SLACK_MCP_XOXC_TOKEN")
	f.StringVar(&cfg.XOXD, "xoxd", "", "Slack browser cookie d (xoxd-...). Env: SLACK_MCP_XOXD_TOKEN")
	f.BoolVar(&cfg.GovSlack, "govslack", false, "Route API calls to slack-gov.com. Env: SLACK_MCP_GOVSLACK")
	f.StringVar(&cfg.Profile, "profile", os.Getenv("SLACK_CLI_PROFILE"), "Named credential profile. Env: SLACK_CLI_PROFILE")
	f.BoolVar(&cfg.NoCache, "no-cache", false, "Skip user/channel cache; only channel/user IDs resolve (no #name/@name lookup)")
	f.BoolVar(&cfg.Raw, "raw", false, "Print tool output verbatim (no JSON pretty-print)")
	f.BoolVarP(&cfg.Verbose, "verbose", "v", false, "Verbose logging to stderr")
	f.DurationVar(&cfg.Timeout, "timeout", 2*time.Minute, "request timeout (2m so one unlisted history 429 can wait Retry-After)")

	root.AddCommand(newAuthCommand(&cfg))
	cmds.AddCommands(root, &cfg)

	root.SetErr(os.Stderr)
	root.SetOut(os.Stdout)
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return fmt.Errorf("%w\n\nRun `%s --help` for usage", err, cmd.CommandPath())
	})

	return root
}
