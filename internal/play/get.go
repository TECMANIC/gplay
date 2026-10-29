package play

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"google.golang.org/api/googleapi"

	"github.com/TECMANIC/gplay/internal/clierror"
)

// PublisherGET performs one GET under androidpublisher v3.
// raw is a URL path plus an optional query, for example
// /applications/com.example/tracks/internal/releases.
// The response body is returned unchanged.
func (c *Client) PublisherGET(ctx context.Context, raw string) ([]byte, error) {
	rel, err := publisherRel(raw)
	if err != nil {
		return nil, err
	}
	endpoint := googleapi.ResolveRelative(c.Pub.BasePath, rel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, clierror.Usage("cannot build the request URL", "")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, clierror.From(err)
	}
	defer resp.Body.Close()
	if err := googleapi.CheckResponse(resp); err != nil {
		return nil, MapAPI(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, clierror.Unexpected("cannot read the response", false)
	}
	return body, nil
}

func publisherRel(raw string) (string, error) {
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "//") {
		return "", clierror.Usage("api path must be a path, not a URL", "example: /applications/com.example/tracks/internal/releases")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host != "" || u.Scheme != "" || u.User != nil {
		return "", clierror.Usage("api path must be a path, not a URL", "example: /applications/com.example/tracks/internal/releases")
	}
	p := u.Path
	if p == "" {
		return "", clierror.Usage("api path is empty", "")
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", clierror.Usage("api path must not contain ..", "")
		}
	}
	cleaned := path.Clean(p)
	if cleaned == "/" || cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return "", clierror.Usage("api path is not under androidpublisher v3", "")
	}
	if strings.HasPrefix(cleaned, "/androidpublisher/") {
		return "", clierror.Usage("pass the path under v3, not the full service path", "example: /applications/com.example/tracks/internal/releases")
	}
	rel := "androidpublisher/v3" + cleaned
	if u.RawQuery != "" {
		rel += "?" + u.RawQuery
	}
	return rel, nil
}
