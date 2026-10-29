package play

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/api/androidpublisher/v3"

	"github.com/TECMANIC/gplay/internal/clierror"
)

// Edit is one Play edit. Discard deletes it unless Commit succeeded.
// Discard uses its own timeout so a canceled command context still cleans up.
type Edit struct {
	Package string
	ID      string
	client  *Client
	closed  bool
}

// Begin opens an edit.
func (c *Client) Begin(ctx context.Context, pkg string) (*Edit, error) {
	ed, err := c.Pub.Edits.Insert(pkg, &androidpublisher.AppEdit{}).Context(ctx).Do()
	if err != nil {
		return nil, MapAPI(err)
	}
	if c.debug != nil {
		fmt.Fprintf(c.debug, "debug: edit id=%s expiryTimeSeconds=%s\n", ed.Id, ed.ExpiryTimeSeconds)
	}
	return &Edit{Package: pkg, ID: ed.Id, client: c}, nil
}

// Discard deletes the edit once. A second call does nothing.
// A nil edit does nothing, so defer Discard() is safe after a failed Begin.
func (e *Edit) Discard() {
	if e == nil || e.closed {
		return
	}
	e.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = e.client.Pub.Edits.Delete(e.Package, e.ID).Context(ctx).Do()
}

// Commit sends the edit. changesNotSentForReview is set only when notSent is true.
// The default in-review behaviour is ERROR_IF_IN_REVIEW, never Google's cancel default.
func (e *Edit) Commit(ctx context.Context, cancelInReview, notSent bool) error {
	if e == nil || e.closed {
		return clierror.Unexpected("no open edit to commit", false)
	}
	call := e.client.Pub.Edits.Commit(e.Package, e.ID)
	if cancelInReview {
		call.ChangesInReviewBehavior("CANCEL_IN_REVIEW_AND_SUBMIT")
	} else {
		call.ChangesInReviewBehavior("ERROR_IF_IN_REVIEW")
	}
	if notSent {
		call.ChangesNotSentForReview(true)
	}
	if _, err := call.Context(ctx).Do(); err != nil {
		return tag(MapAPI(err), e.ID)
	}
	e.closed = true
	return nil
}

// ListTracks returns the tracks in this edit.
func (e *Edit) ListTracks(ctx context.Context) ([]*androidpublisher.Track, error) {
	resp, err := e.client.Pub.Edits.Tracks.List(e.Package, e.ID).Context(ctx).Do()
	if err != nil {
		return nil, tag(MapAPI(err), e.ID)
	}
	return resp.Tracks, nil
}

// ResolveTrack matches name exactly, then case-insensitively, and returns
// the server's canonical track name.
func (e *Edit) ResolveTrack(ctx context.Context, name string) (string, error) {
	tracks, err := e.ListTracks(ctx)
	if err != nil {
		return "", err
	}
	var names []string
	folded := ""
	for _, tr := range tracks {
		if tr == nil || tr.Track == "" {
			continue
		}
		names = append(names, tr.Track)
		if tr.Track == name {
			return tr.Track, nil
		}
		if folded == "" && strings.EqualFold(tr.Track, strings.TrimSpace(name)) {
			folded = tr.Track
		}
	}
	if folded != "" {
		return folded, nil
	}
	hint := "the app has no tracks"
	if len(names) > 0 {
		hint = "known tracks: " + strings.Join(names, ", ")
	}
	return "", tag(clierror.NotFound("unknown track "+name, hint), e.ID)
}

// GetTrack fetches one track. name must already be the canonical server name.
func (e *Edit) GetTrack(ctx context.Context, name string) (*androidpublisher.Track, error) {
	tr, err := e.client.Pub.Edits.Tracks.Get(e.Package, e.ID, name).Context(ctx).Do()
	if err != nil {
		return nil, tag(MapAPI(err), e.ID)
	}
	return tr, nil
}

func tag(err error, editID string) error {
	ce, ok := err.(*clierror.Error)
	if !ok || ce == nil || editID == "" || ce.EditID != "" {
		return err
	}
	cp := *ce
	cp.EditID = editID
	return &cp
}
