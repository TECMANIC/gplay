package auth

import (
	"bytes"
	"encoding/json"

	"github.com/TECMANIC/gplay/internal/clierror"
)

type serviceAccount struct {
	Type         string `json:"type"`
	TokenURI     string `json:"token_uri"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	ProjectID    string `json:"project_id"`
}

// ParseServiceAccount accepts only type=service_account keys whose token_uri
// is the Google token endpoint. external_account and impersonated keys are
// rejected so a repo-supplied file cannot start a subprocess or call an
// arbitrary URL. Parse errors never include the document.
func ParseServiceAccount(raw []byte) (Account, *clierror.Error) {
	if len(raw) > MaxCredentialJSON {
		return Account{}, clierror.Auth("credential JSON exceeds 64 KiB", "")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var doc serviceAccount
	if err := dec.Decode(&doc); err != nil {
		return Account{}, clierror.Auth("credential JSON is not valid", "provide a Google service-account key")
	}
	if doc.Type != "service_account" {
		got := doc.Type
		if got == "" {
			got = "missing"
		}
		return Account{}, clierror.Auth(
			"credential type "+got+" is not accepted",
			"gplay accepts only a service-account JSON key (type service_account)",
		)
	}
	if doc.TokenURI != TokenURI {
		return Account{}, clierror.Auth(
			"token_uri must be https://oauth2.googleapis.com/token",
			"refusing a service-account file that names a different token endpoint",
		)
	}
	if doc.ClientEmail == "" || doc.PrivateKey == "" || doc.ProjectID == "" {
		return Account{}, clierror.Auth(
			"service-account JSON is missing client_email, private_key, or project_id",
			"download a new key from the Cloud Console",
		)
	}
	cpy := make([]byte, len(raw))
	copy(cpy, raw)
	return Account{
		ClientEmail:  doc.ClientEmail,
		ProjectID:    doc.ProjectID,
		PrivateKey:   doc.PrivateKey,
		PrivateKeyID: doc.PrivateKeyID,
		Raw:          cpy,
	}, nil
}
