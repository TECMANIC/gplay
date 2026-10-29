package play

import (
	"context"

	"google.golang.org/api/androidpublisher/v3"
)

// ReleaseStatus lists releases for a track without opening an edit.
// Lifecycle states are returned as Play sends them.
func (c *Client) ReleaseStatus(ctx context.Context, pkg, track string) ([]*androidpublisher.ReleaseSummary, error) {
	parent := "applications/" + pkg + "/tracks/" + track
	resp, err := c.Pub.Applications.Tracks.Releases.List(parent).Context(ctx).Do()
	if err != nil {
		return nil, MapAPI(err)
	}
	if resp.Releases == nil {
		return []*androidpublisher.ReleaseSummary{}, nil
	}
	return resp.Releases, nil
}
