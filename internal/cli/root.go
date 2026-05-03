// Package cli builds the Cobra command tree for the toris binary.
// CLI layer responsibilities:
//   - Parse flags
//   - Load and validate config
//   - Wire up dependencies
//   - Dispatch to control/data plane
//   - Print human-readable or JSON output
//
// No business logic lives here. All heavy work is in internal packages.
package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tobibamidele/toris/internal/config"
	"github.com/tobibamidele/toris/internal/logging"
)

// globalFlags holds values set via persistent flags.
type globalFlags struct {
	cfgFile      string
	logLevel     string
	outputFormat string // "human" | "json"
	dryRun       bool
	force        bool
}

var gFlags globalFlags

// rootCmd is the base Cobra command.
var rootCmd = &cobra.Command{
	Use:   "toris",
	Short: "PostgreSQL backup, failover, and cluster orchestration",
	Long: `toris manages PostgreSQL clusters: backup, restore, failover,
leader election, and a stable single-DSN endpoint for your applications.

Run 'toris help <command>' for details on any command.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute is the entry point called from main.go.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		printError(err)
		os.Exit(1)
	}
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&gFlags.cfgFile, "config", "", "path to config file (default: toris.yaml in $HOME/.toris, /etc/toris, or .)")
	pf.StringVar(&gFlags.logLevel, "log-level", "", "log level override: debug, info, warn, error")
	pf.StringVar(&gFlags.outputFormat, "output", "", "output format: human or json")
	pf.BoolVar(&gFlags.dryRun, "dry-run", false, "perform checks without making changes (for destructive commands)")
	pf.BoolVar(&gFlags.force, "force", false, "skip confirmation prompts (for destructive commands)")

	// Register all subcommands.
	rootCmd.AddCommand(
		newInitCmd(),
		newConfigCmd(),
		newClusterCmd(),
		newNodeCmd(),
		newHealthCmd(),
		newBackupCmd(),
		newRestoreCmd(),
		newLeaderCmd(),
		newPromoteCmd(),
		newDemoteCmd(),
		newRewindCmd(),
		newReseedCmd(),
		newDaemonCmd(),
		newDoctorCmd(),
		newVersionCmd(),
	)
}

// ─── Config loading helper ────────────────────────────────────────────────────

// loadConfig loads and validates the config, applying flag overrides.
// Commands call this to get a *config.Config and *logging.Logger.
func loadConfig() (*config.Config, *logging.Logger, error) {
	overrides := map[string]any{}
	if gFlags.logLevel != "" {
		overrides["log_level"] = gFlags.logLevel
	}
	if gFlags.outputFormat != "" {
		overrides["output_format"] = gFlags.outputFormat
	}

	cfg, err := config.Load(gFlags.cfgFile, overrides)
	if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w", err)
	}
	if err := config.Validate(cfg); err != nil {
		return nil, nil, fmt.Errorf("invalid config: %w", err)
	}

	log, err := logging.New(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return nil, nil, fmt.Errorf("initializing logger: %w", err)
	}

	return cfg, log, nil
}

// ─── Output helpers ───────────────────────────────────────────────────────────

// printJSON serializes v to stdout as indented JSON.
func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// printHuman prints a human-readable line to stdout.
func printHuman(format string, args ...any) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}

// printError prints an error to stderr.
func printError(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
}

// outputResult prints r as JSON or human text based on the --output flag and cfg.
func outputResult(cfg *config.Config, r any) {
	format := cfg.OutputFormat
	if gFlags.outputFormat != "" {
		format = gFlags.outputFormat
	}
	if format == "json" {
		printJSON(r)
	} else {
		printHuman("%v", r)
	}
}

// confirmDestructive prompts the user for confirmation on destructive operations.
// If --force is set, the prompt is skipped.
func confirmDestructive(prompt string) (bool, error) {
	if gFlags.force {
		return true, nil
	}
	fmt.Fprintf(os.Stderr, "%s [yes/N]: ", prompt)
	var resp string
	if _, err := fmt.Scanln(&resp); err != nil {
		return false, nil
	}
	return resp == "yes", nil
}
