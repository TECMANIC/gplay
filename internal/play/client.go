package play

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"cloud.google.com/go/auth"
	"cloud.google.com/go/auth/credentials"
	"google.golang.org/api/androidpublisher/v3"
	"google.golang.org/api/option"
	reporting "google.golang.org/api/playdeveloperreporting/v1beta1"

	"github.com/TECMANIC/gplay/internal/clierror"
	"github.com/TECMANIC/gplay/internal/redact"
	"github.com/TECMANIC/gplay/internal/transport"
)

// Scopes requested for a service-account key.
var Scopes = []string{
	androidpublisher.AndroidpublisherScope,
	reporting.PlaydeveloperreportingScope,
}

// DefaultChunkSize is the production upload chunk size.
const DefaultChunkSize = 8 << 20

// Client talks to androidpublisher and playdeveloperreporting through one transport.
type Client struct {
	Pub       *androidpublisher.Service
	Rep       *reporting.Service
	Guard     *transport.Guard
	debug     io.Writer
	chunkSize int
	http      *http.Client
}

// Options selects credentials and the endpoint.
// StaticToken skips the token exchange. Tests use it so a fake key is never
// sent to a token endpoint. Production leaves it empty.
type Options struct {
	Endpoint    string
	JSON        []byte
	ADC         bool
	StaticToken string
	Debug       io.Writer
	// ChunkSize overrides DefaultChunkSize. Zero keeps the default.
	// Tests pass a multiple of 256 KiB.
	ChunkSize int
}

// New builds both API services on one allowlisting transport.
//
// option.WithHTTPClient returns before the generated client applies
// option.WithAuthCredentialsJSON (google.golang.org/api v0.310.0,
// transport/http.NewClient). Service-account bytes are loaded with
// credentials.NewCredentialsFromJSON(credentials.ServiceAccount, …), which
// rejects every other credential type, and this client attaches the bearer
// token. Token refresh uses the same Guard as API calls.
func New(ctx context.Context, opts Options) (*Client, error) {
	guard := transport.NewGuard(endpointHost(opts.Endpoint), opts.Debug, nil)
	tokenClient := &http.Client{Transport: guard}
	tok, err := tokenSource(opts, tokenClient)
	if err != nil {
		return nil, err
	}
	api := &http.Client{
		Transport: transport.Authed{Guard: guard, Token: tok},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("refusing to follow a redirect")
		},
	}
	clientOpts := []option.ClientOption{
		option.WithHTTPClient(api),
		option.WithLogger(slog.New(slog.DiscardHandler)),
	}
	if opts.Endpoint != "" {
		clientOpts = append(clientOpts, option.WithEndpoint(opts.Endpoint))
	}
	pub, err := androidpublisher.NewService(ctx, clientOpts...)
	if err != nil {
		return nil, clierror.Unexpected("androidpublisher client: "+redact.Scrub(err.Error()), false)
	}
	rep, err := reporting.NewService(ctx, clientOpts...)
	if err != nil {
		return nil, clierror.Unexpected("playdeveloperreporting client: "+redact.Scrub(err.Error()), false)
	}
	return &Client{Pub: pub, Rep: rep, Guard: guard, debug: opts.Debug, chunkSize: opts.ChunkSize, http: api}, nil
}

// ChunkSize is the upload chunk size this client will request.
func (c *Client) ChunkSize() int {
	if c == nil || c.chunkSize == 0 {
		return DefaultChunkSize
	}
	return c.chunkSize
}

func tokenSource(opts Options, tokenClient *http.Client) (transport.TokenFunc, error) {
	if opts.StaticToken != "" {
		return func(*http.Request) (string, error) { return opts.StaticToken, nil }, nil
	}
	det := &credentials.DetectOptions{
		Scopes: Scopes,
		Client: tokenClient,
		Logger: slog.New(slog.DiscardHandler),
	}
	var creds *auth.Credentials
	var err error
	switch {
	case opts.ADC:
		creds, err = credentials.DetectDefault(det)
	case len(opts.JSON) > 0:
		creds, err = credentials.NewCredentialsFromJSON(credentials.ServiceAccount, opts.JSON, det)
	default:
		return nil, clierror.Auth("no credential material", "")
	}
	if err != nil {
		return nil, clierror.Auth("credentials were rejected", redact.Scrub(err.Error()))
	}
	return func(req *http.Request) (string, error) {
		tok, err := creds.Token(req.Context())
		if err != nil {
			return "", err
		}
		if tok == nil || tok.Value == "" {
			return "", fmt.Errorf("empty access token")
		}
		return tok.Value, nil
	}, nil
}

func endpointHost(endpoint string) []string {
	if endpoint == "" {
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	return []string{u.Hostname()}
}

// SearchApps calls Reporting apps.search. It does not open an edit.
func (c *Client) SearchApps(ctx context.Context, pageSize int64) ([]*reporting.GooglePlayDeveloperReportingV1beta1App, error) {
	var out []*reporting.GooglePlayDeveloperReportingV1beta1App
	call := c.Rep.Apps.Search().PageSize(pageSize).Context(ctx)
	for {
		resp, err := call.Do()
		if err != nil {
			return nil, MapAPI(err)
		}
		out = append(out, resp.Apps...)
		if resp.NextPageToken == "" {
			if out == nil {
				out = []*reporting.GooglePlayDeveloperReportingV1beta1App{}
			}
			return out, nil
		}
		call = c.Rep.Apps.Search().PageSize(pageSize).PageToken(resp.NextPageToken).Context(ctx)
	}
}

// ProbeEdit opens an edit and deletes it. Doctor uses this to check app access.
func (c *Client) ProbeEdit(ctx context.Context, pkg string) error {
	ed, err := c.Pub.Edits.Insert(pkg, &androidpublisher.AppEdit{}).Context(ctx).Do()
	if err != nil {
		return MapAPI(err)
	}
	if c.debug != nil {
		fmt.Fprintf(c.debug, "debug: edit id=%s expiryTimeSeconds=%s\n", ed.Id, ed.ExpiryTimeSeconds)
	}
	if err := c.Pub.Edits.Delete(pkg, ed.Id).Context(ctx).Do(); err != nil {
		return MapAPI(err)
	}
	return nil
}

// ReportingProbe calls apps.search with pageSize 1 and returns the response headers.
// The call does not open an edit. Doctor reads the Date header from it.
func (c *Client) ReportingProbe(ctx context.Context) (http.Header, error) {
	resp, err := c.Rep.Apps.Search().PageSize(1).Context(ctx).Do()
	if err != nil {
		return nil, MapAPI(err)
	}
	return resp.Header, nil
}
