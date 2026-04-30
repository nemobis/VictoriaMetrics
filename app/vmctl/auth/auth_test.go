package auth

import (
	"net/http"
	"testing"
)

// --- parseHeaders ---

func TestParseHeadersEmpty(t *testing.T) {
	kvs, err := parseHeaders("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kvs != nil {
		t.Fatalf("expected nil, got %v", kvs)
	}
}

func TestParseHeadersSingle(t *testing.T) {
	kvs, err := parseHeaders("X-Foo: bar")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(kvs) != 1 {
		t.Fatalf("expected 1 header, got %d", len(kvs))
	}
	if kvs[0].key != "X-Foo" || kvs[0].value != "bar" {
		t.Errorf("unexpected kv: %+v", kvs[0])
	}
}

func TestParseHeadersMultiple(t *testing.T) {
	kvs, err := parseHeaders("X-Foo: bar^^X-Baz: qux")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(kvs) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(kvs))
	}
	if kvs[0].key != "X-Foo" || kvs[0].value != "bar" {
		t.Errorf("unexpected kv[0]: %+v", kvs[0])
	}
	if kvs[1].key != "X-Baz" || kvs[1].value != "qux" {
		t.Errorf("unexpected kv[1]: %+v", kvs[1])
	}
}

func TestParseHeadersMissingColon(t *testing.T) {
	_, err := parseHeaders("X-Foo-bar")
	if err == nil {
		t.Fatal("expected error for header without colon, got nil")
	}
}

func TestParseHeadersTrimsSpace(t *testing.T) {
	kvs, err := parseHeaders("  Content-Type :  application/json  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kvs[0].key != "Content-Type" || kvs[0].value != "application/json" {
		t.Errorf("unexpected kv: %+v", kvs[0])
	}
}

// --- WithBasicAuth / WithBearer / WithHeaders ---

func TestWithBasicAuthEmpty(t *testing.T) {
	cfg := &HTTPClientConfig{}
	WithBasicAuth("", "")(cfg)
	if cfg.BasicAuth != nil {
		t.Fatal("expected BasicAuth to be nil when both username and password are empty")
	}
}

func TestWithBasicAuthSet(t *testing.T) {
	cfg := &HTTPClientConfig{}
	WithBasicAuth("user", "pass")(cfg)
	if cfg.BasicAuth == nil {
		t.Fatal("expected BasicAuth to be set")
	}
	if cfg.BasicAuth.Username != "user" || cfg.BasicAuth.Password != "pass" {
		t.Errorf("unexpected BasicAuth: %+v", cfg.BasicAuth)
	}
}

func TestWithBearerEmpty(t *testing.T) {
	cfg := &HTTPClientConfig{}
	WithBearer("")(cfg)
	if cfg.BearerToken != "" {
		t.Fatal("expected BearerToken to remain empty")
	}
}

func TestWithBearerSet(t *testing.T) {
	cfg := &HTTPClientConfig{}
	WithBearer("mytoken")(cfg)
	if cfg.BearerToken != "mytoken" {
		t.Errorf("unexpected BearerToken: %q", cfg.BearerToken)
	}
}

func TestWithHeadersEmpty(t *testing.T) {
	cfg := &HTTPClientConfig{}
	WithHeaders("")(cfg)
	if cfg.Headers != "" {
		t.Fatal("expected Headers to remain empty")
	}
}

func TestWithHeadersSet(t *testing.T) {
	cfg := &HTTPClientConfig{}
	WithHeaders("X-Foo: bar")(cfg)
	if cfg.Headers != "X-Foo: bar" {
		t.Errorf("unexpected Headers: %q", cfg.Headers)
	}
}

// --- Generate ---

func TestGenerateNoAuth(t *testing.T) {
	c, err := Generate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil Config")
	}
	if c.GetAuthHeader() != "" {
		t.Errorf("expected empty auth header, got %q", c.GetAuthHeader())
	}
}

func TestGenerateWithBasicAuth(t *testing.T) {
	c, err := Generate(WithBasicAuth("admin", "secret"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ah := c.GetAuthHeader()
	if ah == "" {
		t.Fatal("expected non-empty auth header")
	}
	// Basic auth header starts with "Basic "
	if len(ah) < 6 || ah[:6] != "Basic " {
		t.Errorf("unexpected auth header format: %q", ah)
	}
}

func TestGenerateWithBearer(t *testing.T) {
	c, err := Generate(WithBearer("tok123"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ah := c.GetAuthHeader()
	if ah != "Bearer tok123" {
		t.Errorf("expected %q, got %q", "Bearer tok123", ah)
	}
}

func TestGenerateWithHeaders(t *testing.T) {
	c, err := Generate(WithHeaders("X-Custom: value"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	c.SetHeaders(req, false)
	if got := req.Header.Get("X-Custom"); got != "value" {
		t.Errorf("expected header X-Custom=value, got %q", got)
	}
}

func TestGenerateBothBasicAndBearerErrors(t *testing.T) {
	_, err := Generate(WithBasicAuth("u", "p"), WithBearer("tok"))
	if err == nil {
		t.Fatal("expected error when both basic auth and bearer token are set")
	}
}

func TestGenerateMissingUsername(t *testing.T) {
	_, err := Generate(WithBasicAuth("", "password-only"))
	// WithBasicAuth only sets BasicAuth when username != "" || password != ""
	// so it will be set but username is empty — NewConfig should error
	if err == nil {
		t.Fatal("expected error for missing username in basic auth")
	}
}

// --- SetHeaders with auth header ---

func TestSetHeadersSetsAuthorization(t *testing.T) {
	c, err := Generate(WithBearer("mytoken"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	c.SetHeaders(req, true)
	if got := req.Header.Get("Authorization"); got != "Bearer mytoken" {
		t.Errorf("expected Authorization=Bearer mytoken, got %q", got)
	}
}

func TestSetHeadersSkipsAuthorizationWhenFalse(t *testing.T) {
	c, err := Generate(WithBearer("mytoken"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	c.SetHeaders(req, false)
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("expected empty Authorization, got %q", got)
	}
}

// --- HTTPClientConfig.NewConfig ---

func TestHTTPClientConfigNewConfig(t *testing.T) {
	hcc := &HTTPClientConfig{
		BearerToken: "tok",
	}
	c, err := hcc.NewConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.GetAuthHeader() != "Bearer tok" {
		t.Errorf("unexpected auth header: %q", c.GetAuthHeader())
	}
}
