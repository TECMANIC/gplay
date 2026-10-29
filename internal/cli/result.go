package cli

import (
	"strconv"

	"github.com/spf13/cobra"
	"google.golang.org/api/androidpublisher/v3"
)

type planStep struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type resultDoc struct {
	DryRun      bool                    `json:"dry_run"`
	Committed   bool                    `json:"committed"`
	EditID      string                  `json:"edit_id,omitempty"`
	VersionCode int64                   `json:"version_code,omitempty"`
	SHA256      string                  `json:"sha256,omitempty"`
	Track       *androidpublisher.Track `json:"track,omitempty"`
	Plan        []planStep              `json:"plan,omitempty"`
}

func writeResult(cmd *cobra.Command, streams IO, doc resultDoc) error {
	row := []string{strconv.FormatBool(doc.DryRun), strconv.FormatBool(doc.Committed), doc.EditID}
	return writeData(cmd, streams, doc, []string{"dry_run", "committed", "edit_id"}, [][]string{row})
}

func appPath(pkg, rest string) string {
	return "/androidpublisher/v3/applications/" + pkg + rest
}
