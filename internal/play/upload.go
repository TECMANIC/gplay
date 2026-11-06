package play

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"

	"google.golang.org/api/googleapi"

	"github.com/TECMANIC/gplay/internal/clierror"
)

const (
	// MaxBundleBytes is 50 GiB, Play's Android App Bundle limit.
	MaxBundleBytes int64 = 50 << 30
	// MaxMappingBytes is 2000 MiB, Play's deobfuscation file limit.
	MaxMappingBytes int64 = 2000 << 20
)

// CheckSize rejects a missing path, a non-regular file, or a file over max.
// The check happens before any API call.
func CheckSize(path, label string, max int64) error {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return clierror.Usage("cannot read "+label, "check that the path exists and is a regular file")
	}
	if fi.Size() > max {
		return clierror.Usage(label+" exceeds the Play size limit", "the file was not sent")
	}
	return nil
}

// UploadBundle sends the AAB and checks Bundle.sha256 against the local file.
// A missing or different server hash is exit 6. The caller deletes the edit.
func (e *Edit) UploadBundle(ctx context.Context, path string) (int64, string, error) {
	if err := CheckSize(path, "AAB", MaxBundleBytes); err != nil {
		return 0, "", err
	}
	f, sum, err := openAndHash(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	bundle, err := e.client.Pub.Edits.Bundles.Upload(e.Package, e.ID).
		Media(f, googleapi.ChunkSize(e.client.ChunkSize()), googleapi.ContentType("application/octet-stream")).
		Context(ctx).Do()
	if err != nil {
		return 0, sum, tag(MapAPI(err), e.ID)
	}
	if bundle.Sha256 == "" || !strings.EqualFold(bundle.Sha256, sum) {
		return bundle.VersionCode, sum, tag(clierror.Validation(
			"bundle SHA-256 does not match the local file",
			"the edit was discarded",
		), e.ID)
	}
	return bundle.VersionCode, sum, nil
}

// UploadMapping sends a ProGuard mapping for versionCode.
func (e *Edit) UploadMapping(ctx context.Context, path string, versionCode int64) error {
	if err := CheckSize(path, "mapping", MaxMappingBytes); err != nil {
		return err
	}
	f, err := os.Open(path) // #nosec G304 -- mapping path is the file the user named
	if err != nil {
		return clierror.Usage("cannot read mapping", "check that the path exists and is a regular file")
	}
	defer f.Close()
	_, err = e.client.Pub.Edits.Deobfuscationfiles.Upload(e.Package, e.ID, versionCode, "proguard").
		Media(f, googleapi.ChunkSize(e.client.ChunkSize()), googleapi.ContentType("application/octet-stream")).
		Context(ctx).Do()
	if err != nil {
		return tag(MapAPI(err), e.ID)
	}
	return nil
}

// Validate asks Play to check the edit. A failure must not be followed by commit.
func (e *Edit) Validate(ctx context.Context) error {
	if _, err := e.client.Pub.Edits.Validate(e.Package, e.ID).Context(ctx).Do(); err != nil {
		return tag(MapAPI(err), e.ID)
	}
	return nil
}

func openAndHash(path string) (*os.File, string, error) {
	f, err := os.Open(path) // #nosec G304 -- AAB path is the file the user named
	if err != nil {
		return nil, "", clierror.Usage("cannot read AAB", "check that the path exists and is a regular file")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		_ = f.Close()
		return nil, "", clierror.Unexpected("cannot hash the AAB", false)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, "", clierror.Unexpected("cannot rewind the AAB", false)
	}
	return f, hex.EncodeToString(h.Sum(nil)), nil
}
