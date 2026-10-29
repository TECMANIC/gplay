package cli

import (
	"context"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/api/androidpublisher/v3"
	"google.golang.org/api/googleapi"

	"github.com/TECMANIC/gplay/internal/clierror"
	"github.com/TECMANIC/gplay/internal/notes"
	"github.com/TECMANIC/gplay/internal/play"
	"github.com/TECMANIC/gplay/internal/policy"
)

func addMutations(cmd *cobra.Command, streams IO) {
	cmd.AddCommand(
		newMutate(streams, "rollout", "Set an in-progress user fraction", policy.OpRollout),
		newMutate(streams, "halt", "Stop serving a staged release", policy.OpHalt),
		newMutate(streams, "resume", "Continue a halted release at the same fraction", policy.OpResume),
		newMutate(streams, "complete", "Roll a release out to all users", policy.OpComplete),
		newPromote(streams),
	)
}

func newMutate(streams IO, use, short, op string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMutate(cmd, streams, op)
		},
	}
	cmd.Flags().String("package", "", "application id, for example com.example")
	cmd.Flags().String("track", "", "track name")
	cmd.Flags().Int64("version-code", 0, "select the release that contains this version code")
	addWriteFlags(cmd)
	if op != policy.OpRollout {
		cmd.Flags().Lookup("fraction").Usage = "rejected; " + use + " does not take a new fraction"
	}
	_ = cmd.MarkFlagRequired("package")
	_ = cmd.MarkFlagRequired("track")
	return cmd
}

func runMutate(cmd *cobra.Command, streams IO, op string) error {
	set, fraction, err := fractionFlag(cmd)
	if err != nil {
		return err
	}
	if op == policy.OpRollout && !set {
		return clierror.Usage("rollout requires --fraction", "full rollout is release complete")
	}
	if op != policy.OpRollout && set {
		return clierror.Usage(cmd.Name()+" does not take --fraction", "full rollout is release complete")
	}
	vcSet := cmd.Flags().Changed("version-code")
	vc, _ := cmd.Flags().GetInt64("version-code")
	if vcSet && vc <= 0 {
		return clierror.Usage("--version-code must be a positive integer", "")
	}
	pol, perr := policy.Load()
	if perr != nil {
		return perr
	}
	flags := writeFlags(cmd, set, fraction)
	if _, derr := policy.Decide(pol, op, flagString(cmd, "track"), flags, nil); derr != nil {
		return derr
	}
	return withWrite(cmd, streams, 60*time.Second, func(ctx context.Context, ed *play.Edit, plan []planStep) (resultDoc, []planStep, error) {
		pkg := flagString(cmd, "package")
		track := flagString(cmd, "track")
		canonical, err := ed.ResolveTrack(ctx, track)
		if err != nil {
			return resultDoc{}, plan, err
		}
		plan = append(plan, planStep{Method: "GET", Path: appPath(pkg, "/edits/"+ed.ID+"/tracks")})
		got, err := ed.GetTrack(ctx, canonical)
		if err != nil {
			return resultDoc{}, plan, err
		}
		plan = append(plan, planStep{Method: "GET", Path: appPath(pkg, "/edits/"+ed.ID+"/tracks/"+canonical)})
		selected, err := selectRelease(got.Releases, vc, vcSet)
		if err != nil {
			return resultDoc{}, plan, err
		}
		if !statusAllowed(op, selected.Status, vcSet) {
			return resultDoc{}, plan, clierror.Usage(
				"cannot "+cmd.Name()+" a release whose status is "+selected.Status,
				"check release status, or pass --version-code",
			)
		}
		rel := cloneRelease(selected)
		applyMutation(op, rel, fraction)
		known := &policy.State{Status: rel.Status, Fraction: rel.UserFraction, FractionKnown: true}
		if _, derr := policy.Decide(pol, op, track, flags, known); derr != nil {
			return resultDoc{}, plan, derr
		}
		updated, err := ed.UpdateTrack(ctx, canonical, rel)
		if err != nil {
			return resultDoc{}, plan, err
		}
		plan = append(plan, planStep{Method: "PUT", Path: appPath(pkg, "/edits/"+ed.ID+"/tracks/"+canonical)})
		if err := ed.Validate(ctx); err != nil {
			return resultDoc{}, plan, err
		}
		plan = append(plan, planStep{Method: "POST", Path: appPath(pkg, "/edits/"+ed.ID+":validate")})
		return resultDoc{EditID: ed.ID, Track: updated, VersionCode: firstCode(rel)}, plan, nil
	})
}

func newPromote(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Copy a release onto another track in the same edit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPromote(cmd, streams)
		},
	}
	cmd.Flags().String("package", "", "application id, for example com.example")
	cmd.Flags().String("from", "", "source track")
	cmd.Flags().String("to", "", "destination track")
	cmd.Flags().Int64("version-code", 0, "select the source release that contains this version code")
	cmd.Flags().String("release-name", "", "destination release name (default: the source name)")
	cmd.Flags().String("notes", "", "replace the copied notes")
	cmd.Flags().String("notes-file", "", "replace the copied notes from a file or directory")
	cmd.Flags().String("language", "en-US", "BCP-47 language for --notes and a single notes file")
	addWriteFlags(cmd)
	_ = cmd.MarkFlagRequired("package")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func runPromote(cmd *cobra.Command, streams IO) error {
	from := flagString(cmd, "from")
	to := flagString(cmd, "to")
	if from == to {
		return clierror.Usage("--from and --to must be different tracks", "")
	}
	set, fraction, err := fractionFlag(cmd)
	if err != nil {
		return err
	}
	parsed, err := notes.Parse(flagString(cmd, "language"), flagString(cmd, "notes"), flagString(cmd, "notes-file"))
	if err != nil {
		return err
	}
	vcSet := cmd.Flags().Changed("version-code")
	vc, _ := cmd.Flags().GetInt64("version-code")
	if vcSet && vc <= 0 {
		return clierror.Usage("--version-code must be a positive integer", "")
	}
	pol, perr := policy.Load()
	if perr != nil {
		return perr
	}
	flags := writeFlags(cmd, set, fraction)
	state, derr := policy.Decide(pol, policy.OpPromote, to, flags, nil)
	if derr != nil {
		return derr
	}
	return withWrite(cmd, streams, 60*time.Second, func(ctx context.Context, ed *play.Edit, plan []planStep) (resultDoc, []planStep, error) {
		pkg := flagString(cmd, "package")
		names, err := ed.ResolveTracks(ctx, from, to)
		if err != nil {
			return resultDoc{}, plan, err
		}
		srcName, dstName := names[0], names[1]
		plan = append(plan, planStep{Method: "GET", Path: appPath(pkg, "/edits/"+ed.ID+"/tracks")})
		src, err := ed.GetTrack(ctx, srcName)
		if err != nil {
			return resultDoc{}, plan, err
		}
		plan = append(plan, planStep{Method: "GET", Path: appPath(pkg, "/edits/"+ed.ID+"/tracks/"+srcName)})
		selected, err := selectRelease(src.Releases, vc, vcSet)
		if err != nil {
			return resultDoc{}, plan, err
		}
		rel := &androidpublisher.TrackRelease{
			Name:         selected.Name,
			Status:       state.Status,
			VersionCodes: append(googleapi.Int64s(nil), selected.VersionCodes...),
			ReleaseNotes: selected.ReleaseNotes,
		}
		if name := flagString(cmd, "release-name"); name != "" {
			rel.Name = name
		}
		if parsed != nil {
			rel.ReleaseNotes = nil
			for _, n := range parsed {
				rel.ReleaseNotes = append(rel.ReleaseNotes, &androidpublisher.LocalizedText{Language: n.Language, Text: n.Text})
			}
		}
		if state.Status == "inProgress" || state.Status == "halted" {
			rel.UserFraction = state.Fraction
		}
		updated, err := ed.UpdateTrack(ctx, dstName, rel)
		if err != nil {
			return resultDoc{}, plan, err
		}
		plan = append(plan, planStep{Method: "PUT", Path: appPath(pkg, "/edits/"+ed.ID+"/tracks/"+dstName)})
		if err := ed.Validate(ctx); err != nil {
			return resultDoc{}, plan, err
		}
		plan = append(plan, planStep{Method: "POST", Path: appPath(pkg, "/edits/"+ed.ID+":validate")})
		return resultDoc{EditID: ed.ID, Track: updated, VersionCode: firstCode(rel)}, plan, nil
	})
}

func withWrite(cmd *cobra.Command, streams IO, timeout time.Duration, fn func(context.Context, *play.Edit, []planStep) (resultDoc, []planStep, error)) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	client, err := clientFor(ctx, cmd, streams)
	if err != nil {
		return err
	}
	pkg := flagString(cmd, "package")
	ed, err := client.Begin(ctx, pkg)
	if err != nil {
		return err
	}
	defer ed.Discard()
	plan := []planStep{{Method: "POST", Path: appPath(pkg, "/edits")}}
	doc, plan, err := fn(ctx, ed, plan)
	if err != nil {
		return err
	}
	committed, plan, err := finish(ctx, ed, pkg, flagBool(cmd, "dry-run"), flagBool(cmd, "cancel-in-review"), flagBool(cmd, "changes-not-sent-for-review"), plan)
	if err != nil {
		return err
	}
	doc.DryRun = flagBool(cmd, "dry-run")
	doc.Committed = committed
	doc.Plan = plan
	doc.EditID = ed.ID
	return writeResult(cmd, streams, doc)
}

func selectRelease(releases []*androidpublisher.TrackRelease, version int64, versionSet bool) (*androidpublisher.TrackRelease, error) {
	var matches []*androidpublisher.TrackRelease
	if versionSet {
		for _, rel := range releases {
			if rel == nil {
				continue
			}
			for _, code := range rel.VersionCodes {
				if code == version {
					matches = append(matches, rel)
					break
				}
			}
		}
		if len(matches) == 0 {
			return nil, clierror.NotFound("version code "+strconv.FormatInt(version, 10)+" is not on the track", "")
		}
	} else {
		for _, rel := range releases {
			if rel != nil && (rel.Status == "inProgress" || rel.Status == "halted") {
				matches = append(matches, rel)
			}
		}
		if len(matches) == 0 {
			return nil, clierror.NotFound("no inProgress or halted release on the track", "pass --version-code")
		}
	}
	if len(matches) > 1 {
		return nil, clierror.Usage("more than one release matches", "pass --version-code")
	}
	return matches[0], nil
}

func statusAllowed(op, current string, versionSet bool) bool {
	switch op {
	case policy.OpRollout:
		return current == "draft" || current == "inProgress" || current == "halted" || current == "completed"
	case policy.OpHalt:
		return current == "inProgress" || (current == "completed" && versionSet)
	case policy.OpResume:
		return current == "halted"
	case policy.OpComplete:
		return current == "inProgress" || current == "halted"
	default:
		return false
	}
}

func applyMutation(op string, rel *androidpublisher.TrackRelease, fraction float64) {
	switch op {
	case policy.OpRollout:
		rel.Status = "inProgress"
		rel.UserFraction = fraction
	case policy.OpHalt:
		rel.Status = "halted"
	case policy.OpResume:
		rel.Status = "inProgress"
	case policy.OpComplete:
		rel.Status = "completed"
		rel.UserFraction = 0
	}
}

func cloneRelease(rel *androidpublisher.TrackRelease) *androidpublisher.TrackRelease {
	cp := *rel
	cp.VersionCodes = append(googleapi.Int64s(nil), rel.VersionCodes...)
	if rel.ReleaseNotes != nil {
		cp.ReleaseNotes = append([]*androidpublisher.LocalizedText(nil), rel.ReleaseNotes...)
	}
	if rel.CountryTargeting != nil {
		ct := *rel.CountryTargeting
		ct.Countries = append([]string(nil), rel.CountryTargeting.Countries...)
		cp.CountryTargeting = &ct
	}
	cp.ForceSendFields = nil
	cp.NullFields = nil
	return &cp
}

func firstCode(rel *androidpublisher.TrackRelease) int64 {
	if rel == nil || len(rel.VersionCodes) == 0 {
		return 0
	}
	return rel.VersionCodes[0]
}
