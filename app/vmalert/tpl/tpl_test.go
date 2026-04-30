package tpl

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// makeRequest creates a synthetic *http.Request for the given URL path.
func makeRequest(path string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	return req
}

// TestFooter_ContainsClosingTags verifies that Footer() renders the expected
// HTML closing tags regardless of the request path.
func TestFooter_ContainsClosingTags(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	got := Footer(req)

	for _, want := range []string{"</main>", "</body>", "</html>"} {
		if !strings.Contains(got, want) {
			t.Errorf("Footer() output missing %q\ngot:\n%s", want, got)
		}
	}
}

// TestHeader_ContainsDoctype verifies that Header() begins with a valid HTML5
// document type declaration.
func TestHeader_ContainsDoctype(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	got := Header(req, nil, "", nil)

	if !strings.Contains(got, "<!DOCTYPE html>") {
		t.Errorf("Header() missing <!DOCTYPE html>\ngot:\n%s", got)
	}
}

// TestHeader_TitleIncluded verifies that when a non-empty title is passed it
// appears in the rendered output.
func TestHeader_TitleIncluded(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	got := Header(req, nil, "My Alerts", nil)

	if !strings.Contains(got, "My Alerts") {
		t.Errorf("Header() should contain title %q\ngot:\n%s", "My Alerts", got)
	}
}

// TestHeader_EmptyTitle verifies that an empty title does not inject the
// separator " - " into the page title.
func TestHeader_EmptyTitle(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	got := Header(req, nil, "", nil)

	if strings.Contains(got, " - ") {
		t.Errorf("Header() with empty title should not contain \" - \"\ngot:\n%s", got)
	}
}

// TestHeader_ErrorIcon verifies that a non-nil userErr causes the error icon
// SVG to be rendered in the nav bar.
func TestHeader_ErrorIcon(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	err := errors.New("config reload failed")
	got := Header(req, nil, "", err)

	// The error icon includes a specific Bootstrap icon class.
	if !strings.Contains(got, "bi-exclamation-triangle-fill") {
		t.Errorf("Header() with non-nil error should contain error icon\ngot:\n%s", got)
	}
}

// TestHeader_NoErrorIcon verifies that a nil userErr does not render the error
// icon.
func TestHeader_NoErrorIcon(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	got := Header(req, nil, "", nil)

	if strings.Contains(got, "bi-exclamation-triangle-fill") {
		t.Errorf("Header() with nil error should NOT contain error icon\ngot:\n%s", got)
	}
}

// TestHeader_ErrorBody verifies that a non-nil error causes the error message
// to appear in the collapsible body area.
func TestHeader_ErrorBody(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	err := errors.New("something went wrong")
	got := Header(req, nil, "", err)

	if !strings.Contains(got, "something went wrong") {
		t.Errorf("Header() with error should render error message in body\ngot:\n%s", got)
	}
}

// TestHeader_NoErrorBody verifies that when there is no error the collapsible
// error card is absent from the output.
func TestHeader_NoErrorBody(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	got := Header(req, nil, "", nil)

	if strings.Contains(got, "reload-groups-error") {
		t.Errorf("Header() with nil error should NOT contain error card\ngot:\n%s", got)
	}
}

// TestHeader_NavItemsRendered verifies that NavItems are included in the nav
// bar output.
func TestHeader_NavItemsRendered(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	items := []NavItem{
		{Name: "Alerts", URL: "alerts"},
		{Name: "Rules", URL: "rules"},
	}
	got := Header(req, items, "Alerts", nil)

	for _, item := range items {
		if !strings.Contains(got, item.Name) {
			t.Errorf("Header() nav items: missing %q in output\ngot:\n%s", item.Name, got)
		}
	}
}

// TestHeader_ActiveNavItem verifies that the currently active nav item has the
// CSS "active" class applied.
func TestHeader_ActiveNavItem(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	items := []NavItem{
		{Name: "Alerts", URL: "alerts"},
		{Name: "Rules", URL: "rules"},
	}
	got := Header(req, items, "Alerts", nil)

	// The active class is added inline when current == item.Name.
	if !strings.Contains(got, "active") {
		t.Errorf("Header() active nav item should have class \"active\"\ngot:\n%s", got)
	}
}

// TestHeader_NavItemAbsoluteURL verifies that absolute URLs in NavItems are
// preserved verbatim (not prefixed).
func TestHeader_NavItemAbsoluteURL(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	items := []NavItem{
		{Name: "Docs", URL: "https://docs.victoriametrics.com"},
	}
	got := Header(req, items, "", nil)

	if !strings.Contains(got, "https://docs.victoriametrics.com") {
		t.Errorf("Header() absolute URL should appear verbatim\ngot:\n%s", got)
	}
}

// TestHeader_NavItemIcon verifies that when an Icon is set for a NavItem the
// SVG <use> element referencing it is rendered.
func TestHeader_NavItemIcon(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	items := []NavItem{
		{Name: "Alerts", URL: "alerts", Icon: "bell"},
	}
	got := Header(req, items, "", nil)

	if !strings.Contains(got, "icons.svg#bell") {
		t.Errorf("Header() icon item: expected icons.svg#bell in output\ngot:\n%s", got)
	}
}

// TestHeader_StaticAssetsLinked verifies that Bootstrap CSS/JS assets are
// referenced in the rendered header.
func TestHeader_StaticAssetsLinked(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	got := Header(req, nil, "", nil)

	for _, asset := range []string{
		"bootstrap.min.css",
		"bootstrap.bundle.min.js",
		"custom.css",
		"custom.js",
	} {
		if !strings.Contains(got, asset) {
			t.Errorf("Header() missing static asset %q\ngot:\n%s", asset, got)
		}
	}
}

// TestErrorIcon_NilError verifies that errorIcon returns an empty-ish string
// (no icon markup) when err is nil.
func TestErrorIcon_NilError(t *testing.T) {
	got := errorIcon(nil)
	if strings.Contains(got, "bi-exclamation-triangle-fill") {
		t.Errorf("errorIcon(nil) should not contain icon markup\ngot: %s", got)
	}
}

// TestErrorIcon_NonNilError verifies that errorIcon returns icon markup when
// err is non-nil.
func TestErrorIcon_NonNilError(t *testing.T) {
	got := errorIcon(errors.New("boom"))
	if !strings.Contains(got, "bi-exclamation-triangle-fill") {
		t.Errorf("errorIcon(err) should contain icon markup\ngot: %s", got)
	}
}

// TestErrorBody_NilError verifies that errorBody returns no card markup when
// err is nil.
func TestErrorBody_NilError(t *testing.T) {
	got := errorBody(nil)
	if strings.Contains(got, "card") {
		t.Errorf("errorBody(nil) should not contain card markup\ngot: %s", got)
	}
}

// TestErrorBody_NonNilError verifies that errorBody renders the error message
// inside the card body.
func TestErrorBody_NonNilError(t *testing.T) {
	msg := "disk full"
	got := errorBody(errors.New(msg))
	if !strings.Contains(got, msg) {
		t.Errorf("errorBody(err) should contain error message %q\ngot: %s", msg, got)
	}
}

// TestFooter_WriteHeader verifies that WriteFooter produces the same output as
// Footer for the same request.
func TestFooter_WriteHeader(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	var sb strings.Builder
	WriteFooter(&sb, req)
	fromWrite := sb.String()
	fromFunc := Footer(req)
	if fromWrite != fromFunc {
		t.Errorf("WriteFooter output differs from Footer()\nWriteFooter: %q\nFooter: %q", fromWrite, fromFunc)
	}
}

// TestHeader_WriteHeader verifies that WriteHeader produces the same output as
// Header for the same inputs.
func TestHeader_WriteHeader(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	items := []NavItem{{Name: "Alerts", URL: "alerts"}}
	var sb strings.Builder
	WriteHeader(&sb, req, items, "Alerts", nil)
	fromWrite := sb.String()
	fromFunc := Header(req, items, "Alerts", nil)
	if fromWrite != fromFunc {
		t.Errorf("WriteHeader output differs from Header()\nWriteHeader: %q\nHeader: %q", fromWrite, fromFunc)
	}
}

// TestNavItem_NoIconSkipsSVG verifies that when Icon is empty the SVG element
// is not rendered.
func TestNavItem_NoIconSkipsSVG(t *testing.T) {
	req := makeRequest("/vmalert/alerts")
	items := []NavItem{
		{Name: "Rules", URL: "rules"},
	}
	got := Header(req, items, "", nil)

	if strings.Contains(got, "<svg") {
		t.Errorf("Header() with no icon should not render <svg>\ngot:\n%s", got)
	}
}
