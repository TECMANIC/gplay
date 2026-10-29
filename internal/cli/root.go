// Package cli is the gplay command tree.
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/TECMANIC/gplay/internal/clierror"
	"github.com/TECMANIC/gplay/internal/output"
	"github.com/TECMANIC/gplay/internal/redact"
)

const (
	modulePath = "github.com/TECMANIC/gplay"
	toolID     = "TECMANIC/gplay"
	rootShort  = "gplay — safety-first Google Play Developer API CLI for AI agents (TECMANIC)"
)

// Build metadata. main sets these from ldflags; tests see the defaults.
var (
	buildVersion = "v0.1.0-dev"
	buildCommit  = "none"
	buildDate    = "unknown"
)

// SetBuildInfo overrides the version line. Empty values keep the default.
func SetBuildInfo(version, commit, date string) {
	if version != "" {
		buildVersion = version
	}
	if commit != "" {
		buildCommit = commit
	}
	if date != "" {
		buildDate = date
	}
}

// IO is the process boundary. Endpoint and StaticToken exist so tests can
// point at a fake server. They are not flags: a repo must not be able to
// redirect Play traffic by passing an argument.
type IO struct {
	Stdout io.Writer
	Stderr io.Writer
	// Endpoint, when set, replaces the Google API base URL.
	Endpoint string
	// StaticToken, when set, is sent as the bearer token and the
	// service-account key is not exchanged. Production leaves this empty.
	StaticToken string
}

// Execute runs one invocation and returns the process exit code.
// stdout receives nothing when the code is non-zero.
func Execute(ctx context.Context, args []string, streams IO) int {
	if streams.Stdout == nil {
		streams.Stdout = os.Stdout
	}
	if streams.Stderr == nil {
		streams.Stderr = os.Stderr
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := newRoot(streams)
	root.SetArgs(args)
	root.SetErr(streams.Stderr)
	var stdout bytes.Buffer
	root.SetOut(&stdout)

	err := root.ExecuteContext(ctx)
	if err != nil {
		ce := scrubError(normalizeErr(err))
		clierror.Write(streams.Stderr, ce)
		return ce.ExitCode
	}
	if _, werr := streams.Stdout.Write(stdout.Bytes()); werr != nil {
		clierror.Write(streams.Stderr, clierror.Unexpected("writing stdout failed", false))
		return 1
	}
	return 0
}

// normalizeErr turns cobra's plain parse errors into exit 2.
// Errors that already use the contract pass through, including exit 1.
func normalizeErr(err error) *clierror.Error {
	var ce *clierror.Error
	if errors.As(err, &ce) && ce != nil {
		return ce
	}
	if err == nil || err.Error() == "" {
		return clierror.Unexpected("empty error", false)
	}
	if isCobraUsage(err.Error()) {
		return clierror.Usage(err.Error(), "run gplay --help")
	}
	return clierror.From(err)
}

func isCobraUsage(msg string) bool {
	switch {
	case strings.HasPrefix(msg, "unknown command "),
		strings.HasPrefix(msg, "unknown flag"),
		strings.HasPrefix(msg, "unknown shorthand"),
		strings.HasPrefix(msg, "invalid argument "),
		strings.HasPrefix(msg, "required flag"),
		strings.Contains(msg, "arg(s)"):
		return true
	default:
		return false
	}
}

func scrubError(e *clierror.Error) *clierror.Error {
	if e == nil {
		return e
	}
	cp := *e
	cp.Message = redact.Scrub(cp.Message)
	cp.Hint = redact.Scrub(cp.Hint)
	cp.GoogleReason = redact.Scrub(cp.GoogleReason)
	cp.GoogleStatus = redact.Scrub(cp.GoogleStatus)
	return &cp
}

func newRoot(streams IO) *cobra.Command {
	root := &cobra.Command{
		Use:           "gplay",
		Short:         rootShort,
		Long:          rootShort + "\n\nNot affiliated with Google. Module " + modulePath + ".",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}
	flags := root.PersistentFlags()
	flags.Bool("debug", false, "log method, URL, status, and latency; never bodies, tokens, or keys")
	flags.String("output", "", "json or table (default: json when stdout is not a terminal)")
	flags.String("credentials", "", "path to a service-account JSON file, or 'adc'")

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		outputFlag, _ := cmd.Flags().GetString("output")
		if _, err := output.WantJSON(streams.Stdout, outputFlag); err != nil {
			return clierror.Usage(err.Error(), "pass --output json or --output table")
		}
		return nil
	}
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return clierror.Usage(err.Error(), "see '"+cmd.CommandPath()+" --help'")
	})

	root.AddCommand(
		newVersionCmd(),
		newSchemaCmd(),
		newAuthCmd(streams),
		newAppsCmd(streams),
		newTracksCmd(streams),
		newReleaseCmd(streams),
	)
	return root
}

func flagString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func flagBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

func debugEnabled(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("debug")
	return v
}

func wantJSON(cmd *cobra.Command, streams IO) (bool, error) {
	return output.WantJSON(streams.Stdout, flagString(cmd, "output"))
}

func warnf(cmd *cobra.Command, format string, args ...any) {
	fmt.Fprintf(cmd.ErrOrStderr(), "warning: "+format+"\n", args...)
}
