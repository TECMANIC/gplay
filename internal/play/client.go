package play

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
	debug     io.Writer
	chunkSize int
	http      *http.Client
	stop      func()
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
	// Stop unregisters the process signal handler. Discard calls it before
	// the cleanup request so a second SIGINT is delivered to the process.
	Stop func()
	// Base replaces http.DefaultTransport under the allowlist. Tests set it
	// so a subject-token host is answered locally. Production leaves it nil.
	Base http.RoundTripper
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
	hosts, adcJSON, err := prepareADC(opts)
	if err != nil {
		return nil, err
	}
	guard := transport.NewGuard(hosts, opts.Debug, opts.Base)
	for _, h := range endpointHost(opts.Endpoint) {
		guard.AllowHTTP(h)
	}
	tokenClient := &http.Client{Transport: guard}
	tok, err := tokenSource(opts, tokenClient, adcJSON)
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
	return &Client{Pub: pub, Rep: rep, debug: opts.Debug, chunkSize: opts.ChunkSize, http: api, stop: opts.Stop}, nil
}

// ChunkSize is the upload chunk size this client will request.
func (c *Client) ChunkSize() int {
	if c == nil || c.chunkSize == 0 {
		return DefaultChunkSize
	}
	return c.chunkSize
}

func tokenSource(opts Options, tokenClient *http.Client, adcJSON []byte) (transport.TokenFunc, error) {
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
	hint := "check the service-account key"
	switch {
	case opts.ADC:
		hint = "check the application default credentials"
		// CredentialsJSON is set by prepareADC. DetectDefault returns before
		// the GCE metadata branch when those bytes are present. The field is
		// deprecated because it does not validate the document; prepareADC
		// already checked credential_source.url.
		//lint:ignore SA1019 subject-token URL is checked in prepareADC; this field is what skips the metadata client
		det.CredentialsJSON = adcJSON
		creds, err = credentials.DetectDefault(det)
	case len(opts.JSON) > 0:
		creds, err = credentials.NewCredentialsFromJSON(credentials.ServiceAccount, opts.JSON, det)
	default:
		return nil, clierror.Auth("no credential material", "")
	}
	if err != nil {
		return nil, clierror.Auth("credentials were rejected", hint)
	}
	return func(req *http.Request) (string, error) {
		tok, terr := creds.Token(req.Context())
		if terr != nil {
			return "", transport.Rejected(terr)
		}
		if tok == nil || tok.Value == "" {
			return "", transport.Rejected(fmt.Errorf("empty access token"))
		}
		return tok.Value, nil
	}, nil
}

// prepareADC reads the ADC file when that source is selected and returns
// the extra allowlist hosts plus the file bytes. The bytes are passed as
// DetectOptions.CredentialsJSON, so DetectDefault never reaches GCE metadata.
// A service-account client gets neither the ADC hosts nor a subject-token host.
func prepareADC(opts Options) (hosts []string, raw []byte, err error) {
	hosts = guardHosts(opts.Endpoint, opts.ADC, "")
	if !opts.ADC {
		return hosts, nil, nil
	}
	raw, err = readADC()
	if err != nil {
		return nil, nil, err
	}
	host, herr := subjectTokenHost(raw)
	if herr != nil {
		return nil, nil, herr
	}
	if host != "" {
		hosts = append(hosts, host)
	}
	return hosts, raw, nil
}

// guardHosts is the extra allowlist. ADC adds the STS and IAM Credentials
// hosts and, when the file has one, the subject-token host. subjectHost is
// ignored unless adc is set.
func guardHosts(endpoint string, adc bool, subjectHost string) []string {
	extra := endpointHost(endpoint)
	if !adc {
		return extra
	}
	extra = append(extra, transport.ADCHosts...)
	if subjectHost != "" {
		extra = append(extra, subjectHost)
	}
	return extra
}

func readADC() ([]byte, error) {
	if path := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); path != "" {
		raw, err := os.ReadFile(path) // #nosec G304 G703 -- path is GOOGLE_APPLICATION_CREDENTIALS, the ADC file the user named
		if err != nil {
			return nil, clierror.Auth("cannot read application default credentials", "check GOOGLE_APPLICATION_CREDENTIALS")
		}
		return raw, nil
	}
	path := wellKnownADC()
	raw, err := os.ReadFile(path) // #nosec G304 -- well-known gcloud ADC path, not a caller-supplied file
	if err != nil {
		return nil, clierror.Auth(
			"GCE metadata application default credentials are refused",
			"the metadata client bypasses the host allowlist; set GOOGLE_APPLICATION_CREDENTIALS to a workload-identity file",
		)
	}
	return raw, nil
}

func wellKnownADC() string {
	const name = "application_default_credentials.json"
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "gcloud", name)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(string(os.PathSeparator), "gcloud", name)
	}
	return filepath.Join(home, ".config", "gcloud", name)
}

// subjectTokenHost returns the credential_source.url host when the ADC file
// has one. The URL must be https, and the host must be
// actions.githubusercontent.com or a subdomain of it. Any other host is exit 3.
func subjectTokenHost(raw []byte) (string, *clierror.Error) {
	var doc struct {
		CredentialSource *struct {
			URL string `json:"url"`
		} `json:"credential_source"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.CredentialSource == nil || doc.CredentialSource.URL == "" {
		return "", nil
	}
	u, err := url.Parse(doc.CredentialSource.URL)
	host := ""
	if err == nil {
		host = strings.ToLower(u.Hostname())
	}
	if err != nil || u.Scheme != "https" || host == "" || u.User != nil {
		return "", clierror.Auth(
			"ADC subject-token URL must be https and have no user info",
			"credential_source.url has to be https://actions.githubusercontent.com or a subdomain",
		)
	}
	if host != "actions.githubusercontent.com" && !strings.HasSuffix(host, ".actions.githubusercontent.com") {
		return "", clierror.Auth(
			"ADC subject-token host is not allowlisted",
			"credential_source.url must be actions.githubusercontent.com or a subdomain",
		)
	}
	return host, nil
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
