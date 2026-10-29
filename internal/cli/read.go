package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/api/androidpublisher/v3"
	reporting "google.golang.org/api/playdeveloperreporting/v1beta1"

	"github.com/TECMANIC/gplay/internal/auth"
	"github.com/TECMANIC/gplay/internal/clierror"
	"github.com/TECMANIC/gplay/internal/output"
	"github.com/TECMANIC/gplay/internal/play"
)

func newAppsCmd(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "Read apps from the Play Developer Reporting API",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return clierror.Usage("apps requires a subcommand", "see '"+cmd.CommandPath()+" --help'")
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List Play apps visible to the service account",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			client, err := clientFor(ctx, cmd, streams)
			if err != nil {
				return err
			}
			apps, err := client.SearchApps(ctx, 1000)
			if err != nil {
				return err
			}
			body := struct {
				Apps any `json:"apps"`
			}{Apps: apps}
			return writeData(cmd, streams, body, []string{"package", "name"}, appRows(apps))
		},
	})
	return cmd
}

func newTracksCmd(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tracks",
		Short: "Read Play tracks inside a short-lived edit",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return clierror.Usage("tracks requires a subcommand", "see '"+cmd.CommandPath()+" --help'")
		},
	}
	cmd.AddCommand(newTracksList(streams), newTracksGet(streams))
	return cmd
}

func newTracksList(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tracks for a package",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withReadEdit(cmd, streams, func(ctx context.Context, ed *play.Edit) (any, []string, [][]string, error) {
				tracks, err := ed.ListTracks(ctx)
				if err != nil {
					return nil, nil, nil, err
				}
				body := struct {
					Tracks any `json:"tracks"`
				}{Tracks: tracks}
				var rows [][]string
				for _, tr := range tracks {
					if tr != nil {
						rows = append(rows, []string{tr.Track})
					}
				}
				return body, []string{"track"}, rows, nil
			})
		},
	}
	cmd.Flags().String("package", "", "application id, for example com.example")
	_ = cmd.MarkFlagRequired("package")
	return cmd
}

func newTracksGet(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Fetch one track, resolved against tracks.list",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name := flagString(cmd, "track")
			return withReadEdit(cmd, streams, func(ctx context.Context, ed *play.Edit) (any, []string, [][]string, error) {
				canonical, err := ed.ResolveTrack(ctx, name)
				if err != nil {
					return nil, nil, nil, err
				}
				tr, err := ed.GetTrack(ctx, canonical)
				if err != nil {
					return nil, nil, nil, err
				}
				return tr, []string{"track", "status"}, [][]string{{tr.Track, releaseStatus(tr)}}, nil
			})
		},
	}
	cmd.Flags().String("package", "", "application id, for example com.example")
	cmd.Flags().String("track", "", "track name as returned by tracks list")
	_ = cmd.MarkFlagRequired("package")
	_ = cmd.MarkFlagRequired("track")
	return cmd
}

func newReleaseCmd(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Create and manage track releases",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return clierror.Usage("release requires a subcommand", "see '"+cmd.CommandPath()+" --help'")
		},
	}
	cmd.AddCommand(newReleaseStatus(streams), newReleaseCreate(streams))
	addMutations(cmd, streams)
	return cmd
}

func newReleaseStatus(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "List release lifecycle states without opening an edit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			client, err := clientFor(ctx, cmd, streams)
			if err != nil {
				return err
			}
			releases, err := client.ReleaseStatus(ctx, flagString(cmd, "package"), flagString(cmd, "track"))
			if err != nil {
				return err
			}
			body := struct {
				Releases any `json:"releases"`
			}{Releases: releases}
			var rows [][]string
			for _, r := range releases {
				rows = append(rows, []string{r.ReleaseName, r.ReleaseLifecycleState})
			}
			return writeData(cmd, streams, body, []string{"release", "state"}, rows)
		},
	}
	cmd.Flags().String("package", "", "application id, for example com.example")
	cmd.Flags().String("track", "", "track name")
	_ = cmd.MarkFlagRequired("package")
	_ = cmd.MarkFlagRequired("track")
	return cmd
}

func withReadEdit(cmd *cobra.Command, streams IO, fn func(context.Context, *play.Edit) (any, []string, [][]string, error)) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()
	client, err := clientFor(ctx, cmd, streams)
	if err != nil {
		return err
	}
	ed, err := client.Begin(ctx, flagString(cmd, "package"))
	if err != nil {
		return err
	}
	defer ed.Discard()
	body, header, rows, err := fn(ctx, ed)
	if err != nil {
		return err
	}
	return writeData(cmd, streams, body, header, rows)
}

func clientFor(ctx context.Context, cmd *cobra.Command, streams IO) (*play.Client, error) {
	res, err := resolve(cmd)
	if err != nil {
		return nil, err
	}
	auth.WarnFileMode(res.Path, cmd.ErrOrStderr())
	return openClient(ctx, cmd, streams, res)
}

func writeData(cmd *cobra.Command, streams IO, body any, header []string, rows [][]string) error {
	asJSON, err := wantJSON(cmd, streams)
	if err != nil {
		return clierror.Usage(err.Error(), "pass --output json or --output table")
	}
	if asJSON {
		return output.JSON(cmd.OutOrStdout(), body)
	}
	if rows == nil {
		rows = [][]string{}
	}
	return output.Table(cmd.OutOrStdout(), header, rows)
}

func appRows(apps []*reporting.GooglePlayDeveloperReportingV1beta1App) [][]string {
	rows := [][]string{}
	for _, a := range apps {
		if a != nil {
			rows = append(rows, []string{a.PackageName, a.DisplayName})
		}
	}
	return rows
}

func releaseStatus(tr *androidpublisher.Track) string {
	if tr == nil || len(tr.Releases) == 0 || tr.Releases[0] == nil {
		return ""
	}
	return tr.Releases[0].Status
}
