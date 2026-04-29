package remoteread

// Tests for app/vmalert/remoteread/init.go
//
// Commit coverage:
//   e58dde692  lib/httputils: parse URL before creating HTTP transport (#6820)
//               -> TestInit_InvalidURL verifies that bad URLs are rejected by
//                  the httputil.CheckURL call in Init().
//   b97916276  app/vmalert: adds idleConnTimeout flags and retry trivial
//              network errors (#6382)
//               -> TestInit_ValidURL verifies that a successful Init() returns
//                  a non-nil QuerierBuilder (transport creation path).
//   6ff1de89a  vmalert: fix alert states restoration (#7624)
//               -> TestInit_EmptyAddr verifies that Init() returns (nil, nil)
//                  when -remoteRead.url is not set, which is the prerequisite
//                  for the restoration-skip path in the caller.
//   d7b506291  app/vmalert: support DNS SRV record in `-remoteWrite.url` (#6299)
//               -> TestInit_ValidURL covers that a plain http:// address is
//                  accepted, and TestInit_InvalidURL gates the bad-scheme path.
//   1c7abd313  docs: fix flag type in descriptions (#9979)
//               -> TestInitSecretFlags_HidesURL / TestInitSecretFlags_ShowsURL
//                  exercise the InitSecretFlags() logic toggled by
//                  -remoteRead.showURL.

import (
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/flagutil"
)

// resetFlags restores the package-level flag vars to safe defaults so that
// tests are independent of each other.  The vars are unexported pointers to
// values registered with flag.String / flag.Bool etc., so we overwrite the
// pointed-to values directly.
func resetFlags() {
	*addr = ""
	*showRemoteReadURL = false
	*headers = ""
	*basicAuthUsername = ""
	*basicAuthPassword = ""
	*basicAuthPasswordFile = ""
	*bearerToken = ""
	*bearerTokenFile = ""
	*tlsInsecureSkipVerify = false
	*tlsCertFile = ""
	*tlsKeyFile = ""
	*tlsCAFile = ""
	*tlsServerName = ""
	*oauth2ClientID = ""
	*oauth2ClientSecret = ""
	*oauth2ClientSecretFile = ""
	*oauth2EndpointParams = ""
	*oauth2TokenURL = ""
	*oauth2Scopes = ""
}

// ---------------------------------------------------------------------------
// TestInit_EmptyAddr
//
// When -remoteRead.url is empty Init() must return (nil, nil) so that callers
// can skip the remote-read path entirely (alert-state restoration relies on
// this behaviour – commit 6ff1de89a).
// ---------------------------------------------------------------------------
func TestInit_EmptyAddr(t *testing.T) {
	resetFlags()

	qb, err := Init()
	if err != nil {
		t.Fatalf("expected no error for empty addr, got: %v", err)
	}
	if qb != nil {
		t.Fatalf("expected nil QuerierBuilder for empty addr, got: %T", qb)
	}
}

// ---------------------------------------------------------------------------
// TestInit_ValidURL
//
// When a well-formed HTTP address is provided Init() must succeed and return
// a non-nil QuerierBuilder.
// ---------------------------------------------------------------------------
func TestInit_ValidURL(t *testing.T) {
	resetFlags()
	*addr = "http://127.0.0.1:8428"

	qb, err := Init()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if qb == nil {
		t.Fatal("expected non-nil QuerierBuilder")
	}
}

// ---------------------------------------------------------------------------
// TestInit_ValidHTTPSURL
//
// HTTPS addresses must also be accepted.
// ---------------------------------------------------------------------------
func TestInit_ValidHTTPSURL(t *testing.T) {
	resetFlags()
	*addr = "https://victoria-metrics.example.com:8428"

	qb, err := Init()
	if err != nil {
		t.Fatalf("unexpected error for HTTPS addr: %v", err)
	}
	if qb == nil {
		t.Fatal("expected non-nil QuerierBuilder for HTTPS addr")
	}
}

// ---------------------------------------------------------------------------
// TestInit_InvalidURL
//
// An unparseable URL must cause Init() to return an error that mentions the
// "-remoteRead.url" flag name so that users can identify the offending flag
// (commit e58dde692 – URL validation was added to prevent obscure transport
// errors).
// ---------------------------------------------------------------------------
func TestInit_InvalidURL(t *testing.T) {
	resetFlags()
	// A raw control character makes url.Parse return an error.
	*addr = "http://\x00invalid"

	_, err := Init()
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
	if !strings.Contains(err.Error(), "remoteRead.url") {
		t.Fatalf("error message should mention -remoteRead.url, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestInit_InvalidOAuth2EndpointParams
//
// Malformed JSON in -remoteRead.oauth2.endpointParams must be rejected before
// any network connection is attempted.
// ---------------------------------------------------------------------------
func TestInit_InvalidOAuth2EndpointParams(t *testing.T) {
	resetFlags()
	*addr = "http://127.0.0.1:8428"
	*oauth2EndpointParams = `{not valid json}`

	_, err := Init()
	if err == nil {
		t.Fatal("expected error for invalid oauth2 endpoint params JSON, got nil")
	}
	if !strings.Contains(err.Error(), "oauth2.endpointParams") {
		t.Fatalf("error should mention oauth2.endpointParams, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestInit_ValidOAuth2EndpointParams
//
// Valid JSON in -remoteRead.oauth2.endpointParams must not cause an error.
// ---------------------------------------------------------------------------
func TestInit_ValidOAuth2EndpointParams(t *testing.T) {
	resetFlags()
	*addr = "http://127.0.0.1:8428"
	*oauth2EndpointParams = `{"audience":"https://example.com"}`

	qb, err := Init()
	if err != nil {
		t.Fatalf("unexpected error for valid oauth2 endpoint params: %v", err)
	}
	if qb == nil {
		t.Fatal("expected non-nil QuerierBuilder")
	}
}

// ---------------------------------------------------------------------------
// TestInit_EmptyOAuth2EndpointParams
//
// An empty string for -remoteRead.oauth2.endpointParams must be treated as
// "no params" (flagutil.ParseJSONMap returns nil, nil for empty input).
// ---------------------------------------------------------------------------
func TestInit_EmptyOAuth2EndpointParams(t *testing.T) {
	resetFlags()
	*addr = "http://127.0.0.1:8428"
	*oauth2EndpointParams = ""

	qb, err := Init()
	if err != nil {
		t.Fatalf("unexpected error for empty oauth2 endpoint params: %v", err)
	}
	if qb == nil {
		t.Fatal("expected non-nil QuerierBuilder")
	}
}

// ---------------------------------------------------------------------------
// TestInit_WithBasicAuth
//
// Providing basic-auth credentials must not prevent a successful init.
// ---------------------------------------------------------------------------
func TestInit_WithBasicAuth(t *testing.T) {
	resetFlags()
	*addr = "http://127.0.0.1:8428"
	*basicAuthUsername = "alice"
	*basicAuthPassword = "s3cr3t"

	qb, err := Init()
	if err != nil {
		t.Fatalf("unexpected error with basic auth: %v", err)
	}
	if qb == nil {
		t.Fatal("expected non-nil QuerierBuilder with basic auth")
	}
}

// ---------------------------------------------------------------------------
// TestInit_WithBearerToken
//
// A bearer token must not prevent a successful init.
// ---------------------------------------------------------------------------
func TestInit_WithBearerToken(t *testing.T) {
	resetFlags()
	*addr = "http://127.0.0.1:8428"
	*bearerToken = "mytoken"

	qb, err := Init()
	if err != nil {
		t.Fatalf("unexpected error with bearer token: %v", err)
	}
	if qb == nil {
		t.Fatal("expected non-nil QuerierBuilder with bearer token")
	}
}

// ---------------------------------------------------------------------------
// TestInit_WithHeaders
//
// Custom headers in the ^^ separated format must be parsed correctly.
// ---------------------------------------------------------------------------
func TestInit_WithHeaders(t *testing.T) {
	resetFlags()
	*addr = "http://127.0.0.1:8428"
	*headers = "X-Custom-Header:value1^^X-Another:value2"

	qb, err := Init()
	if err != nil {
		t.Fatalf("unexpected error with custom headers: %v", err)
	}
	if qb == nil {
		t.Fatal("expected non-nil QuerierBuilder with custom headers")
	}
}

// ---------------------------------------------------------------------------
// TestInit_TLSInsecureSkipVerify
//
// Setting tlsInsecureSkipVerify=true must not cause an error for a plain
// HTTP address (the flag merely affects the TLS config when TLS is in use).
// ---------------------------------------------------------------------------
func TestInit_TLSInsecureSkipVerify(t *testing.T) {
	resetFlags()
	*addr = "http://127.0.0.1:8428"
	*tlsInsecureSkipVerify = true

	qb, err := Init()
	if err != nil {
		t.Fatalf("unexpected error with tlsInsecureSkipVerify=true: %v", err)
	}
	if qb == nil {
		t.Fatal("expected non-nil QuerierBuilder with tlsInsecureSkipVerify=true")
	}
}

// ---------------------------------------------------------------------------
// TestInitSecretFlags_HidesURL
//
// When showRemoteReadURL is false (default), InitSecretFlags() must register
// "remoteRead.url" as a secret flag so it is redacted in exported metrics.
// We verify the side-effect by checking that the flag is listed as secret
// after the call.
// ---------------------------------------------------------------------------
func TestInitSecretFlags_HidesURL(t *testing.T) {
	resetFlags()
	flagutil.UnregisterAllSecretFlags()
	t.Cleanup(flagutil.UnregisterAllSecretFlags)

	// showRemoteReadURL=false -> URL should be registered as secret.
	*showRemoteReadURL = false
	InitSecretFlags()

	// RegisterSecretFlag stores the name lowercased; IsSecretFlag looks up the
	// map with the supplied string as-is (no auto-lowercasing), so we must
	// pass the lowercase form here.
	if !flagutil.IsSecretFlag("remoteread.url") {
		t.Fatal("expected remoteread.url to be registered as a secret flag when showRemoteReadURL=false")
	}
}

// ---------------------------------------------------------------------------
// TestInitSecretFlags_ShowsURL
//
// When showRemoteReadURL is true, InitSecretFlags() must NOT register the
// "remoteRead.url" flag as secret, so the URL remains visible in exported
// metrics.
// ---------------------------------------------------------------------------
func TestInitSecretFlags_ShowsURL(t *testing.T) {
	resetFlags()
	flagutil.UnregisterAllSecretFlags()
	t.Cleanup(flagutil.UnregisterAllSecretFlags)

	*showRemoteReadURL = true
	// The if-block in InitSecretFlags is skipped, so remoteread.url must NOT
	// be in the secret-flags map after this call.
	InitSecretFlags()

	// RegisterSecretFlag stores lowercased names; check both forms.
	if flagutil.IsSecretFlag("remoteread.url") {
		t.Fatal("expected remoteread.url NOT to be a secret flag when showRemoteReadURL=true")
	}
}

// ---------------------------------------------------------------------------
// TestParseJSONMap_Integration
//
// Verifies the JSON-map parsing that Init() delegates to
// flagutil.ParseJSONMap, used for -remoteRead.oauth2.endpointParams.
// This test directly exercises the same code path without going through Init()
// so that specific error messages can be inspected.
// ---------------------------------------------------------------------------
func TestParseJSONMap_Integration(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
		wantLen int
	}{
		{
			name:    "empty string returns nil map",
			input:   "",
			wantErr: false,
			wantLen: 0,
		},
		{
			name:    "valid single entry",
			input:   `{"key":"value"}`,
			wantErr: false,
			wantLen: 1,
		},
		{
			name:    "valid multiple entries",
			input:   `{"a":"1","b":"2","c":"3"}`,
			wantErr: false,
			wantLen: 3,
		},
		{
			name:    "invalid JSON",
			input:   `{not json}`,
			wantErr: true,
		},
		{
			name:    "JSON array instead of object",
			input:   `["a","b"]`,
			wantErr: true,
		},
		{
			name:    "integer values – not valid map[string]string",
			input:   `{"key":42}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := flagutil.ParseJSONMap(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for input %q: %v", tc.input, err)
			}
			if len(m) != tc.wantLen {
				t.Fatalf("expected map length %d, got %d (map=%v)", tc.wantLen, len(m), m)
			}
		})
	}
}
