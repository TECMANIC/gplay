package testkey

import "encoding/json"

// ServiceAccountJSON is a service-account document for tests.
// The private key is not a usable RSA key. keyID is embedded so tests can
// assert it never appears in process output.
func ServiceAccountJSON(keyID string) []byte {
	doc := map[string]string{ // #nosec G101 -- fixture for redaction tests; not a real credential
		"type":           "service_account",
		"project_id":     "proj",
		"private_key_id": keyID,
		"private_key":    "-----BEGIN PRIVATE KEY-----\nya29.supersecrettoken\n-----END PRIVATE KEY-----\n",
		"client_email":   "app@proj.iam.gserviceaccount.com",
		"token_uri":      "https://oauth2.googleapis.com/token",
	}
	b, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return b
}
