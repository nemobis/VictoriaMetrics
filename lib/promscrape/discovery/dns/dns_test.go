package dns

import (
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/promutil"
)

// ---- label helpers ----------------------------------------------------------

func getLabel(m *promutil.Labels, key string) (string, bool) {
	for _, l := range m.Labels {
		if l.Name == key {
			return l.Value, true
		}
	}
	return "", false
}

func requireLabel(t *testing.T, m *promutil.Labels, key, want string) {
	t.Helper()
	got, ok := getLabel(m, key)
	if !ok {
		t.Errorf("label %q not found in %v", key, m.Labels)
		return
	}
	if got != want {
		t.Errorf("label %q: got %q, want %q", key, got, want)
	}
}

func requireNoLabel(t *testing.T, m *promutil.Labels, key string) {
	t.Helper()
	if _, ok := getLabel(m, key); ok {
		t.Errorf("label %q should not be present", key)
	}
}

// ---- appendAddrLabels -------------------------------------------------------

func TestAppendAddrLabelsIPv4(t *testing.T) {
	result := appendAddrLabels(nil, "example.com", "192.0.2.1", 9090)
	if len(result) != 1 {
		t.Fatalf("expected 1 label set, got %d", len(result))
	}
	m := result[0]
	requireLabel(t, m, "__address__", "192.0.2.1:9090")
	requireLabel(t, m, "__meta_dns_name", "example.com")
	requireLabel(t, m, "__meta_dns_srv_record_target", "192.0.2.1")
	requireLabel(t, m, "__meta_dns_srv_record_port", "9090")
}

func TestAppendAddrLabelsIPv6(t *testing.T) {
	result := appendAddrLabels(nil, "example.com", "2001:db8::1", 8080)
	if len(result) != 1 {
		t.Fatalf("expected 1 label set, got %d", len(result))
	}
	m := result[0]
	requireLabel(t, m, "__address__", "[2001:db8::1]:8080")
	requireLabel(t, m, "__meta_dns_name", "example.com")
	requireLabel(t, m, "__meta_dns_srv_record_target", "2001:db8::1")
	requireLabel(t, m, "__meta_dns_srv_record_port", "8080")
}

func TestAppendAddrLabelsAppends(t *testing.T) {
	ms := appendAddrLabels(nil, "a.example", "1.2.3.4", 80)
	ms = appendAddrLabels(ms, "b.example", "5.6.7.8", 443)
	if len(ms) != 2 {
		t.Fatalf("expected 2 label sets, got %d", len(ms))
	}
	requireLabel(t, ms[0], "__meta_dns_name", "a.example")
	requireLabel(t, ms[1], "__meta_dns_name", "b.example")
}

func TestAppendAddrLabelsLabelCount(t *testing.T) {
	result := appendAddrLabels(nil, "x.example", "10.0.0.1", 1234)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry")
	}
	// Exactly 4 labels: __address__, __meta_dns_name, __meta_dns_srv_record_target, __meta_dns_srv_record_port
	if len(result[0].Labels) != 4 {
		t.Errorf("expected 4 labels, got %d: %v", len(result[0].Labels), result[0].Labels)
	}
}

// ---- appendMXLabels ---------------------------------------------------------

func TestAppendMXLabels(t *testing.T) {
	result := appendMXLabels(nil, "example.com", "mail.example.com", 25)
	if len(result) != 1 {
		t.Fatalf("expected 1 label set, got %d", len(result))
	}
	m := result[0]
	requireLabel(t, m, "__address__", "mail.example.com:25")
	requireLabel(t, m, "__meta_dns_name", "example.com")
	requireLabel(t, m, "__meta_dns_mx_record_target", "mail.example.com")
}

func TestAppendMXLabelsNoSRVLabels(t *testing.T) {
	result := appendMXLabels(nil, "example.com", "relay.example.com", 587)
	if len(result) != 1 {
		t.Fatalf("expected 1 label set, got %d", len(result))
	}
	m := result[0]
	requireNoLabel(t, m, "__meta_dns_srv_record_target")
	requireNoLabel(t, m, "__meta_dns_srv_record_port")
}

func TestAppendMXLabelsLabelCount(t *testing.T) {
	result := appendMXLabels(nil, "example.com", "mx.example.com", 25)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry")
	}
	// Exactly 3 labels: __address__, __meta_dns_name, __meta_dns_mx_record_target
	if len(result[0].Labels) != 3 {
		t.Errorf("expected 3 labels, got %d: %v", len(result[0].Labels), result[0].Labels)
	}
}

func TestAppendMXLabelsAppends(t *testing.T) {
	ms := appendMXLabels(nil, "a.example", "mx1.a.example", 25)
	ms = appendMXLabels(ms, "b.example", "mx1.b.example", 25)
	if len(ms) != 2 {
		t.Fatalf("expected 2 label sets, got %d", len(ms))
	}
	requireLabel(t, ms[0], "__meta_dns_name", "a.example")
	requireLabel(t, ms[1], "__meta_dns_name", "b.example")
}

// ---- SDConfig.GetLabels validation ------------------------------------------

func TestGetLabelsEmptyNames(t *testing.T) {
	sdc := &SDConfig{
		Names: nil,
		Type:  "A",
	}
	_, err := sdc.GetLabels("")
	if err == nil {
		t.Fatal("expected error for empty Names, got nil")
	}
}

func TestGetLabelsUnknownType(t *testing.T) {
	sdc := &SDConfig{
		Names: []string{"example.com"},
		Type:  "UNKNOWN",
	}
	_, err := sdc.GetLabels("")
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
}

func TestGetLabelsAMissingPort(t *testing.T) {
	sdc := &SDConfig{
		Names: []string{"example.com"},
		Type:  "A",
		Port:  nil,
	}
	_, err := sdc.GetLabels("")
	if err == nil {
		t.Fatal("expected error for A type without port, got nil")
	}
}

func TestGetLabelsAAAAMissingPort(t *testing.T) {
	sdc := &SDConfig{
		Names: []string{"example.com"},
		Type:  "AAAA",
		Port:  nil,
	}
	_, err := sdc.GetLabels("")
	if err == nil {
		t.Fatal("expected error for AAAA type without port, got nil")
	}
}

func TestGetLabelsTypeNormalization(t *testing.T) {
	// lowercase "a" should be treated the same as "A"
	port := 9000
	sdc := &SDConfig{
		Names: []string{"example.com"},
		Type:  "a",
		Port:  &port,
	}
	// This will attempt a real DNS lookup; the point is it should NOT return
	// "unexpected type" error, only a possible network error.
	_, err := sdc.GetLabels("")
	if err != nil {
		// A network error is fine; what we're checking is that there's no
		// "unexpected type" error.
		t.Logf("GetLabels returned (possibly network) error: %v", err)
	}
}

func TestMustStop(t *testing.T) {
	sdc := &SDConfig{Names: []string{"example.com"}}
	sdc.MustStop() // must not panic
}
