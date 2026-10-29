package auth

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/TECMANIC/gplay/internal/clierror"
)

const (
	// TokenURI is the only token endpoint a service-account file may name.
	TokenURI = "https://oauth2.googleapis.com/token" // #nosec G101 -- public token endpoint, not a secret
	// ToolID is written into the config file and required on read.
	ToolID = "TECMANIC/gplay"
	// MaxCredentialJSON is the cap for GPLAY_CREDENTIALS_JSON.
	MaxCredentialJSON = 64 << 10

	SourceFlag    = "flag"
	SourceEnv     = "env"
	SourceEnvJSON = "env_json"
	SourceProfile = "profile"
	SourceADC     = "adc"
)

// Account is a parsed service-account key. Raw is the original bytes.
type Account struct {
	ClientEmail  string
	ProjectID    string
	PrivateKey   string
	PrivateKeyID string
	Raw          []byte
}

// Fingerprint is the hex SHA-256 of the PKIX public key derived from
// private_key, prefixed with sha256:. A rotated key changes it. An
// unparsable PEM returns "" so the field is omitted. The private-key
// bytes are not hashed.
func (a Account) Fingerprint() string {
	pub, ok := publicKey(a.PrivateKey)
	if !ok {
		return ""
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func publicKey(pemText string) (any, bool) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, false
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return publicHalf(key)
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return &key.PublicKey, true
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return &key.PublicKey, true
	}
	return nil, false
}

func publicHalf(key any) (any, bool) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey, true
	case *ecdsa.PrivateKey:
		return &k.PublicKey, true
	default:
		return nil, false
	}
}

// Resolved is the one credential source that will be used.
type Resolved struct {
	Source  string
	Account Account
	Path    string
	ADC     bool
}

// Resolve picks the first configured credential source and stops.
// A configured source that cannot be read is an error; later sources are not tried.
func Resolve(flag string, flagSet bool) (*Resolved, *clierror.Error) {
	if flagSet {
		return loadConfigured(SourceFlag, flag)
	}
	if v, ok := os.LookupEnv("GPLAY_CREDENTIALS"); ok {
		return loadConfigured(SourceEnv, v)
	}
	if v, ok := os.LookupEnv("GPLAY_CREDENTIALS_JSON"); ok {
		return loadJSON(SourceEnvJSON, v)
	}
	path, err := ConfigPath()
	if err != nil {
		return nil, clierror.Auth("cannot locate the gplay config directory", err.Error())
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, clierror.Auth(
				"no credentials configured",
				"pass --credentials <key.json>, set GPLAY_CREDENTIALS to a file path or 'adc', or run gplay auth login",
			)
		}
		return nil, clierror.Config("cannot read "+path, "")
	}
	return loadProfile(path)
}

func loadConfigured(source, value string) (*Resolved, *clierror.Error) {
	if value == "adc" {
		return &Resolved{Source: SourceADC, ADC: true}, nil
	}
	if value == "" {
		return nil, clierror.Auth("credential source "+source+" is empty", "pass a service-account JSON path or 'adc'")
	}
	raw, err := os.ReadFile(value) // #nosec G304 -- credential path is the source the user configured
	if err != nil {
		return nil, clierror.Auth("cannot read credential file", "check that the path exists and is readable")
	}
	acc, aerr := ParseServiceAccount(raw)
	if aerr != nil {
		return nil, aerr
	}
	return &Resolved{Source: source, Account: acc, Path: value}, nil
}

func loadJSON(source, value string) (*Resolved, *clierror.Error) {
	if len(value) > MaxCredentialJSON {
		return nil, clierror.Auth("GPLAY_CREDENTIALS_JSON exceeds 64 KiB", "point GPLAY_CREDENTIALS at a 0600 file instead")
	}
	if value == "" {
		return nil, clierror.Auth("GPLAY_CREDENTIALS_JSON is empty", "set it to the service-account JSON document")
	}
	acc, err := ParseServiceAccount([]byte(value))
	if err != nil {
		return nil, err
	}
	return &Resolved{Source: source, Account: acc}, nil
}

type configFile struct {
	Tool        string `json:"tool"`
	Credentials string `json:"credentials"`
}

func loadProfile(path string) (*Resolved, *clierror.Error) {
	f, err := os.Open(path) // #nosec G304 -- config path comes from os.UserConfigDir
	if err != nil {
		return nil, clierror.Config("cannot read "+path, "")
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var cfg configFile
	if err := dec.Decode(&cfg); err != nil {
		return nil, clierror.Config("gplay config is not valid JSON", "expected {\"tool\":\""+ToolID+"\",\"credentials\":\"/absolute/key.json\"}")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, clierror.Config("gplay config contains extra data", "")
	}
	if cfg.Tool != ToolID {
		return nil, clierror.Config(
			"refusing gplay config: tool id is missing or not "+ToolID,
			"this file may belong to another gplay binary; fix the tool field or remove the file",
		)
	}
	if cfg.Credentials == "" || !filepath.IsAbs(cfg.Credentials) {
		return nil, clierror.Config("credentials path in config must be absolute", "run gplay auth login --credentials <key.json>")
	}
	raw, err := os.ReadFile(cfg.Credentials)
	if err != nil {
		return nil, clierror.Auth("cannot read credential file named by the config", "check the path stored by gplay auth login")
	}
	acc, aerr := ParseServiceAccount(raw)
	if aerr != nil {
		return nil, aerr
	}
	return &Resolved{Source: SourceProfile, Account: acc, Path: cfg.Credentials}, nil
}

// ConfigPath is os.UserConfigDir()/gplay/config.json.
func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gplay", "config.json"), nil
}

// SaveLogin stores an absolute path to keyPath. The key is not copied.
func SaveLogin(keyPath string) (string, *clierror.Error) {
	abs, err := filepath.Abs(keyPath)
	if err != nil {
		return "", clierror.Usage("cannot resolve credential path", "")
	}
	raw, err := os.ReadFile(abs) // #nosec G304 -- credential path passed to auth login
	if err != nil {
		return "", clierror.Auth("cannot read credential file", "check that the path exists and is readable")
	}
	if _, aerr := ParseServiceAccount(raw); aerr != nil {
		return "", aerr
	}
	dest, err := ConfigPath()
	if err != nil {
		return "", clierror.Config("cannot locate the gplay config directory", "")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", clierror.Config("cannot create the gplay config directory", "")
	}
	body, err := json.MarshalIndent(configFile{Tool: ToolID, Credentials: abs}, "", "  ")
	if err != nil {
		return "", clierror.Unexpected("cannot encode config", false)
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(dest), "config-*.json")
	if err != nil {
		return "", clierror.Config("cannot write gplay config", "")
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", clierror.Config("cannot set config file permissions", "")
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return "", clierror.Config("cannot write gplay config", "")
	}
	if err := tmp.Close(); err != nil {
		return "", clierror.Config("cannot write gplay config", "")
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return "", clierror.Config("cannot replace gplay config", "")
	}
	ok = true
	return abs, nil
}

// WarnFileMode prints a stderr warning when a credential file is group-
// or world-readable. Windows has no Unix mode bits; the check is skipped.
func WarnFileMode(path string, w io.Writer) {
	if path == "" || runtime.GOOS == "windows" || w == nil {
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	if st.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(w, "warning: credential file is readable by group or others: %s\n", path)
	}
}

// FileModeLoose reports the same condition WarnFileMode warns about.
func FileModeLoose(path string) bool {
	if path == "" || runtime.GOOS == "windows" {
		return false
	}
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return st.Mode().Perm()&0o077 != 0
}
