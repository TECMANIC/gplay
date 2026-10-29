package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/TECMANIC/gplay/internal/clierror"
)

func newAPICmd(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api GET <path>",
		Short: "GET a path under the androidpublisher v3 API",
		Long:  "v0.1 accepts GET only. The path is relative to androidpublisher v3, for example /applications/com.example/tracks/internal/releases. Other methods are rejected. --output table is rejected.",
		Args:  cobra.ExactArgs(2),
		Annotations: map[string]string{
			"success": "The Play response body, unchanged. Not re-encoded.",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "GET" {
				return clierror.Usage("gplay api is GET-only in v0.1", "example: gplay api GET /applications/com.example/tracks/internal/releases")
			}
			asJSON, err := wantJSON(cmd, streams)
			if err != nil {
				return clierror.Usage(err.Error(), "pass --output json or --output table")
			}
			if !asJSON {
				return clierror.Usage("api GET does not support --output table", "pass --output json")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			client, err := clientFor(ctx, cmd, streams)
			if err != nil {
				return err
			}
			body, err := client.PublisherGET(ctx, args[1])
			if err != nil {
				return err
			}
			if _, err := cmd.OutOrStdout().Write(body); err != nil {
				return clierror.Unexpected("writing stdout failed", false)
			}
			return nil
		},
	}
	return cmd
}
