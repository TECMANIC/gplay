package cli

import (
	"context"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/TECMANIC/gplay/internal/clierror"
	"github.com/TECMANIC/gplay/internal/play"
	"github.com/TECMANIC/gplay/internal/policy"
)

const defaultAAB = "build/app/outputs/bundle/release/app-release.aab"

func newUploadCmd(streams IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upload",
		Short: "Upload an Android App Bundle into an edit",
		Long: "upload --dry-run really uploads the bundle, validates the edit, and then deletes it. " +
			"It spends quota and bandwidth. The bundle does not remain available, because the edit is deleted rather than committed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpload(cmd, streams)
		},
	}
	cmd.Flags().String("package", "", "application id, for example com.example")
	cmd.Flags().String("aab", defaultAAB, "path to the Android App Bundle")
	cmd.Flags().String("mapping", "", "ProGuard mapping file uploaded as deobfuscationFiles/proguard")
	cmd.Flags().Bool("commit", false, "commit the edit after a successful validate")
	cmd.Flags().Bool("dry-run", false, "upload and validate, then delete the edit; the bundle is still sent")
	cmd.Flags().Bool("changes-not-sent-for-review", false, "set changesNotSentForReview=true on commit")
	cmd.Flags().Bool("cancel-in-review", false, "commit with CANCEL_IN_REVIEW_AND_SUBMIT; requires allowCancelInReview")
	_ = cmd.MarkFlagRequired("package")
	return cmd
}

func runUpload(cmd *cobra.Command, streams IO) error {
	commit := flagBool(cmd, "commit")
	dry := flagBool(cmd, "dry-run")
	if commit && dry {
		return clierror.Usage("--commit and --dry-run cannot be combined", "pick one")
	}
	aab := flagString(cmd, "aab")
	mapping := flagString(cmd, "mapping")
	if err := play.CheckSize(aab, "AAB", play.MaxBundleBytes); err != nil {
		return err
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
	if _, err := policy.Decide(pol, policy.OpUpload, "", policy.Flags{
		CancelInReview: flagBool(cmd, "cancel-in-review"),
		DryRun:         dry,
	}, nil); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
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
	version, sum, err := ed.UploadBundle(ctx, aab)
	plan = append(plan, planStep{Method: "POST", Path: "/upload/androidpublisher/v3/applications/" + pkg + "/edits/" + ed.ID + "/bundles"})
	if err != nil {
		return err
	}
	if mapping != "" {
		if err := ed.UploadMapping(ctx, mapping, version); err != nil {
			return err
		}
		plan = append(plan, planStep{Method: "POST", Path: appPath(pkg, "/edits/"+ed.ID+"/apks/"+itoa(version)+"/deobfuscationFiles/proguard")})
	}
	if err := ed.Validate(ctx); err != nil {
		return err
	}
	plan = append(plan, planStep{Method: "POST", Path: appPath(pkg, "/edits/"+ed.ID+":validate")})

	committed := false
	if commit {
		if err := ed.Commit(ctx, flagBool(cmd, "cancel-in-review"), flagBool(cmd, "changes-not-sent-for-review")); err != nil {
			return err
		}
		committed = true
		plan = append(plan, planStep{Method: "POST", Path: appPath(pkg, "/edits/"+ed.ID+":commit")})
	} else {
		ed.Discard()
		plan = append(plan, planStep{Method: "DELETE", Path: appPath(pkg, "/edits/"+ed.ID)})
	}
	return writeResult(cmd, streams, resultDoc{
		DryRun:      dry,
		Committed:   committed,
		EditID:      ed.ID,
		VersionCode: version,
		SHA256:      sum,
		Plan:        plan,
	})
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
