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

func newReleaseCreate(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a release, uploading an AAB in the same edit when --aab is set",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReleaseCreate(cmd, streams)
		},
	}
	cmd.Flags().String("package", "", "application id, for example com.example")
	cmd.Flags().String("track", "", "track name")
	cmd.Flags().String("aab", "", "Android App Bundle to upload in this edit")
	cmd.Flags().Int64("version-code", 0, "version code already uploaded, or the code expected from --aab")
	cmd.Flags().String("mapping", "", "ProGuard mapping file")
	cmd.Flags().String("release-name", "", "release name (default: the decimal version code)")
	cmd.Flags().String("notes", "", "release notes in --language")
	cmd.Flags().String("notes-file", "", "a notes file, or a directory of <bcp47>.txt files")
	cmd.Flags().String("language", "en-US", "BCP-47 language for --notes and a single notes file")
	addWriteFlags(cmd)
	_ = cmd.MarkFlagRequired("package")
	_ = cmd.MarkFlagRequired("track")
	return cmd
}

// mediaTimeout is 30 minutes when the command uploads an AAB or a mapping,
// and 60 seconds otherwise. SPEC §4 gives every media call the long deadline.
func mediaTimeout(aab, mapping string) time.Duration {
	if aab != "" || mapping != "" {
		return 30 * time.Minute
	}
	return 60 * time.Second
}

func addWriteFlags(cmd *cobra.Command) {
	cmd.Flags().String("status", "", "draft, inProgress, halted, or completed")
	cmd.Flags().String("fraction", "", "user fraction in (0, 1); 1 is rejected")
	cmd.Flags().Bool("dry-run", false, "perform the edit, including any upload, then validate and delete")
	cmd.Flags().Bool("changes-not-sent-for-review", false, "set changesNotSentForReview=true on commit")
	cmd.Flags().Bool("cancel-in-review", false, "commit with CANCEL_IN_REVIEW_AND_SUBMIT; requires allowCancelInReview")
	cmd.Flags().Bool("allow-production", false, "acknowledge a production-track write")
	cmd.Flags().String("allow-production-fraction", "", "exact resulting fraction, or 'completed'")
}

func runReleaseCreate(cmd *cobra.Command, streams IO) error {
	pkg := flagString(cmd, "package")
	track := flagString(cmd, "track")
	aab := flagString(cmd, "aab")
	mapping := flagString(cmd, "mapping")
	vcSet := cmd.Flags().Changed("version-code")
	vc, _ := cmd.Flags().GetInt64("version-code")
	if aab == "" && !vcSet {
		return clierror.Usage("release create requires --aab or --version-code", "")
	}
	if vcSet && vc <= 0 {
		return clierror.Usage("--version-code must be a positive integer", "")
	}
	set, fraction, err := fractionFlag(cmd)
	if err != nil {
		return err
	}
	parsed, err := notes.Parse(flagString(cmd, "language"), flagString(cmd, "notes"), flagString(cmd, "notes-file"))
	if err != nil {
		return err
	}
	if aab != "" {
		if err := play.CheckSize(aab, "AAB", play.MaxBundleBytes); err != nil {
			return err
		}
	}
	if mapping != "" {
		if err := play.CheckSize(mapping, "mapping", play.MaxMappingBytes); err != nil {
			return err
		}
	}
	pol, perr := policy.Load()
	if perr != nil {
		return perr
	}
	flags := writeFlags(cmd, set, fraction)
	state, derr := policy.Decide(pol, policy.OpCreate, track, flags, nil)
	if derr != nil {
		return derr
	}

	timeout := mediaTimeout(aab, mapping)
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	client, err := clientFor(ctx, cmd, streams)
	if err != nil {
		return err
	}
	ed, err := client.Begin(ctx, pkg)
	if err != nil {
		return err
	}
	defer ed.Discard()
	plan := []planStep{{Method: "POST", Path: appPath(pkg, "/edits")}}

	canonical, err := ed.ResolveTrack(ctx, track)
	if err != nil {
		return err
	}
	plan = append(plan, planStep{Method: "GET", Path: appPath(pkg, "/edits/"+ed.ID+"/tracks")})

	version := vc
	var sum string
	if aab != "" {
		uploaded, hash, err := ed.UploadBundle(ctx, aab)
		plan = append(plan, planStep{Method: "POST", Path: "/upload/androidpublisher/v3/applications/" + pkg + "/edits/" + ed.ID + "/bundles"})
		if err != nil {
			return err
		}
		if vcSet && uploaded != vc {
			return clierror.Usage("--version-code does not match the uploaded bundle", "omit --version-code or pass the bundle's version code")
		}
		version = uploaded
		sum = hash
	}
	if mapping != "" {
		if err := ed.UploadMapping(ctx, mapping, version); err != nil {
			return err
		}
		plan = append(plan, planStep{Method: "POST", Path: appPath(pkg, "/edits/"+ed.ID+"/apks/"+strconv.FormatInt(version, 10)+"/deobfuscationFiles/proguard")})
	}
	name := flagString(cmd, "release-name")
	if name == "" {
		name = strconv.FormatInt(version, 10)
	}
	rel := &androidpublisher.TrackRelease{
		Name:         name,
		Status:       state.Status,
		VersionCodes: googleapi.Int64s{version},
	}
	if state.Status == "inProgress" || state.Status == "halted" {
		rel.UserFraction = state.Fraction
	}
	if len(parsed) > 0 {
		for _, n := range parsed {
			rel.ReleaseNotes = append(rel.ReleaseNotes, &androidpublisher.LocalizedText{Language: n.Language, Text: n.Text})
		}
	}
	updated, err := ed.UpdateTrack(ctx, canonical, rel)
	if err != nil {
		return err
	}
	plan = append(plan, planStep{Method: "PUT", Path: appPath(pkg, "/edits/"+ed.ID+"/tracks/"+canonical)})
	if err := ed.Validate(ctx); err != nil {
		return err
	}
	plan = append(plan, planStep{Method: "POST", Path: appPath(pkg, "/edits/"+ed.ID+":validate")})
	committed, plan, err := finish(ctx, ed, pkg, flags.DryRun, flags.CancelInReview, flagBool(cmd, "changes-not-sent-for-review"), plan)
	if err != nil {
		return err
	}
	return writeResult(cmd, streams, resultDoc{
		DryRun:      flags.DryRun,
		Committed:   committed,
		EditID:      ed.ID,
		VersionCode: version,
		SHA256:      sum,
		Track:       updated,
		Plan:        plan,
	})
}

func finish(ctx context.Context, ed *play.Edit, pkg string, dry, cancelInReview, notSent bool, plan []planStep) (bool, []planStep, error) {
	if dry {
		ed.Discard()
		return false, append(plan, planStep{Method: "DELETE", Path: appPath(pkg, "/edits/"+ed.ID)}), nil
	}
	if err := ed.Commit(ctx, cancelInReview, notSent); err != nil {
		return false, plan, err
	}
	return true, append(plan, planStep{Method: "POST", Path: appPath(pkg, "/edits/"+ed.ID+":commit")}), nil
}

func writeFlags(cmd *cobra.Command, fractionSet bool, fraction float64) policy.Flags {
	return policy.Flags{
		Status:          flagString(cmd, "status"),
		FractionSet:     fractionSet,
		Fraction:        fraction,
		AllowProduction: flagBool(cmd, "allow-production"),
		AllowFraction:   flagString(cmd, "allow-production-fraction"),
		CancelInReview:  flagBool(cmd, "cancel-in-review"),
		DryRun:          flagBool(cmd, "dry-run"),
	}
}

func fractionFlag(cmd *cobra.Command) (bool, float64, error) {
	if !cmd.Flags().Changed("fraction") {
		return false, 0, nil
	}
	raw := flagString(cmd, "fraction")
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return false, 0, clierror.Usage("--fraction is not a number", "example: --fraction 0.1")
	}
	return true, v, nil
}
