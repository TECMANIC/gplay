package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/TECMANIC/gplay/internal/auth"
	"github.com/TECMANIC/gplay/internal/clierror"
	"github.com/TECMANIC/gplay/internal/output"
	"github.com/TECMANIC/gplay/internal/play"
)

func newAuthCmd(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage service-account credentials",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return clierror.Usage("auth requires a subcommand", "see '"+cmd.CommandPath()+" --help'")
		},
	}
	cmd.AddCommand(newAuthLogin(streams), newAuthStatus(streams), newAuthDoctor(streams))
	return cmd
}

func newAuthLogin(streams IO) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Store the path to a service-account key (the key is not copied)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("credentials") {
				return clierror.Usage("auth login requires --credentials <key.json>", "pass the path to a service-account JSON file")
			}
			path := flagString(cmd, "credentials")
			if path == "adc" {
				return clierror.Usage("auth login stores a key file path", "ADC has nothing to store; pass a JSON file or use --credentials adc at call time")
			}
			saved, err := auth.SaveLogin(path)
			if err != nil {
				return err
			}
			auth.WarnFileMode(saved, cmd.ErrOrStderr())
			return writeAuth(cmd, streams, loginDoc{Tool: toolID, Credentials: saved})
		},
	}
}

func newAuthStatus(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show which credential source is in use",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := resolve(cmd)
			if err != nil {
				return err
			}
			auth.WarnFileMode(res.Path, cmd.ErrOrStderr())
			doc := statusDoc{Source: res.Source, Validated: false}
			if !res.ADC {
				doc.ClientEmail = res.Account.ClientEmail
				doc.ProjectID = res.Account.ProjectID
				doc.KeyFingerprint = res.Account.Fingerprint()
			}
			validate, _ := cmd.Flags().GetBool("validate")
			if validate {
				ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
				defer cancel()
				client, err := openClient(ctx, cmd, streams, res)
				if err != nil {
					return err
				}
				if _, err := client.ReportingProbe(ctx); err != nil {
					return err
				}
				doc.Validated = true
			}
			return writeAuth(cmd, streams, doc)
		},
	}
	cmd.Flags().Bool("validate", false, "call Reporting apps.search; does not open an edit")
	return cmd
}

func newAuthDoctor(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check key permissions, clock, and API access",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := resolve(cmd)
			if err != nil {
				return err
			}
			var doc doctorDoc
			if auth.FileModeLoose(res.Path) {
				msg := "credential file is readable by group or others: " + res.Path
				auth.WarnFileMode(res.Path, cmd.ErrOrStderr())
				doc.Findings = append(doc.Findings, finding{Severity: "warning", Message: msg})
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			client, err := openClient(ctx, cmd, streams, res)
			if err != nil {
				return err
			}
			header, err := client.ReportingProbe(ctx)
			if err != nil {
				return err
			}
			doc.Probes = append(doc.Probes, probe{Name: "reporting", OK: true})
			if f := clockFinding(header); f != nil {
				warnf(cmd, "%s", f.Message)
				doc.Findings = append(doc.Findings, *f)
			}
			pkg := flagString(cmd, "package")
			if pkg == "" {
				doc.Probes = append(doc.Probes, probe{Name: "edit", Skipped: true, Detail: "--package not set"})
			} else {
				if err := client.ProbeEdit(ctx, pkg); err != nil {
					return err
				}
				doc.Probes = append(doc.Probes, probe{Name: "edit", OK: true})
			}
			if doc.Findings == nil {
				doc.Findings = []finding{}
			}
			return writeAuth(cmd, streams, doc)
		},
	}
	cmd.Flags().String("package", "", "package name for the edits.insert and edits.delete probe")
	return cmd
}

type loginDoc struct {
	Tool        string `json:"tool"`
	Credentials string `json:"credentials"`
}

type statusDoc struct {
	Source         string `json:"source"`
	ClientEmail    string `json:"client_email,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
	Validated      bool   `json:"validated"`
}

type doctorDoc struct {
	Findings []finding `json:"findings"`
	Probes   []probe   `json:"probes"`
}

type finding struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type probe struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func clockFinding(h http.Header) *finding {
	if h == nil {
		return nil
	}
	raw := h.Get("Date")
	if raw == "" {
		return &finding{Severity: "warning", Message: "response had no Date header; clock skew was not checked"}
	}
	when, err := http.ParseTime(raw)
	if err != nil {
		return &finding{Severity: "warning", Message: "Date header is not an HTTP date"}
	}
	delta := time.Since(when)
	if delta < 0 {
		delta = -delta
	}
	if delta > 30*time.Second {
		return &finding{Severity: "warning", Message: fmt.Sprintf("clock skew is %s", delta.Truncate(time.Second))}
	}
	return nil
}

func resolve(cmd *cobra.Command) (*auth.Resolved, error) {
	// Resolve returns *clierror.Error. Returning that nil pointer as error
	// would make a successful resolution look like a failure.
	res, err := auth.Resolve(flagString(cmd, "credentials"), cmd.Flags().Changed("credentials"))
	if err != nil {
		return nil, err
	}
	return res, nil
}

func openClient(ctx context.Context, cmd *cobra.Command, streams IO, res *auth.Resolved) (*play.Client, error) {
	opts := play.Options{Endpoint: streams.Endpoint, ChunkSize: streams.ChunkSize}
	if debugEnabled(cmd) {
		opts.Debug = cmd.ErrOrStderr()
	}
	switch {
	case streams.StaticToken != "":
		opts.StaticToken = streams.StaticToken
	case res.ADC:
		opts.ADC = true
	default:
		opts.JSON = res.Account.Raw
	}
	return play.New(ctx, opts)
}

func writeAuth(cmd *cobra.Command, streams IO, v any) error {
	asJSON, err := wantJSON(cmd, streams)
	if err != nil {
		return clierror.Usage(err.Error(), "pass --output json or --output table")
	}
	if asJSON {
		return output.JSON(cmd.OutOrStdout(), v)
	}
	w := cmd.OutOrStdout()
	switch d := v.(type) {
	case loginDoc:
		fmt.Fprintf(w, "tool: %s\ncredentials: %s\n", d.Tool, d.Credentials)
	case statusDoc:
		fmt.Fprintf(w, "source: %s\n", d.Source)
		if d.ClientEmail != "" {
			fmt.Fprintf(w, "client_email: %s\n", d.ClientEmail)
		}
		if d.ProjectID != "" {
			fmt.Fprintf(w, "project_id: %s\n", d.ProjectID)
		}
		if d.KeyFingerprint != "" {
			fmt.Fprintf(w, "key_fingerprint: %s\n", d.KeyFingerprint)
		}
		fmt.Fprintf(w, "validated: %t\n", d.Validated)
	case doctorDoc:
		for _, f := range d.Findings {
			fmt.Fprintf(w, "finding: %s: %s\n", f.Severity, f.Message)
		}
		for _, p := range d.Probes {
			fmt.Fprintf(w, "probe: %s ok=%t skipped=%t %s\n", p.Name, p.OK, p.Skipped, p.Detail)
		}
	default:
		return output.JSON(w, v)
	}
	return nil
}
