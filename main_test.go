package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/openrdap/rdap"
)

// Verify that MockRDAPClient implements the RDAPClient interface
var _ RDAPClient = (*MockRDAPClient)(nil)

type MockRDAPClient struct {
	responses map[string]struct {
		resp *rdap.Response
		err  error
	}
}

func NewMockRDAPClient() *MockRDAPClient {
	return &MockRDAPClient{
		responses: make(map[string]struct {
			resp *rdap.Response
			err  error
		}),
	}
}

func (m *MockRDAPClient) SetResponse(domain string, resp *rdap.Response, err error) {
	m.responses[domain] = struct {
		resp *rdap.Response
		err  error
	}{resp, err}
}

func (m *MockRDAPClient) Do(req *rdap.Request) (*rdap.Response, error) {
	if res, ok := m.responses[req.Query]; ok {
		return res.resp, res.err
	}
	return nil, errors.New("unexpected request")
}

func MockResponse(domain string) *rdap.Response {
	domainObj := &rdap.Domain{}
	resp := &rdap.Response{
		Object: domainObj,
	}
	return resp
}

var ErrDomainNotFound = errors.New("domain not found")

type MockWhoisProvider struct {
	responses map[string]struct {
		result *WhoisResult
		err    error
	}
}

var _ WhoisProvider = (*MockWhoisProvider)(nil)

func NewMockWhoisProvider() *MockWhoisProvider {
	return &MockWhoisProvider{
		responses: make(map[string]struct {
			result *WhoisResult
			err    error
		}),
	}
}

func (m *MockWhoisProvider) SetResponse(domain string, result *WhoisResult, err error) {
	m.responses[domain] = struct {
		result *WhoisResult
		err    error
	}{result, err}
}

func (m *MockWhoisProvider) Query(ctx context.Context, domain string) (*WhoisResult, error) {
	if res, ok := m.responses[domain]; ok {
		return res.result, res.err
	}
	return nil, errors.New("unexpected domain in mock whois client")
}

func TestLookupWithWhois(t *testing.T) {
	tests := []struct {
		name           string
		domain         string
		mockResult     *WhoisResult
		mockError      error
		expectedStatus string
	}{
		{
			name:   "Registered Domain",
			domain: "google.com",
			mockResult: &WhoisResult{
				IsAvailable: boolPtr(false),
				RawText:     "Domain Name: google.com",
			},
			mockError:      nil,
			expectedStatus: StatusRegistered,
		},
		{
			name:   "Available Domain",
			domain: "nonexistent-domain-for-test.com",
			mockResult: &WhoisResult{
				IsAvailable: boolPtr(true),
				RawText:     "",
			},
			mockError:      nil,
			expectedStatus: StatusAvailable,
		},
		{
			name:           "Error in WHOIS lookup",
			domain:         "error-domain.com",
			mockResult:     nil,
			mockError:      errors.New("whois lookup failed"),
			expectedStatus: StatusUnknown,
		},
		{
			name:   "Domain with raw text but no IsAvailable flag",
			domain: "raw-text-domain.com",
			mockResult: &WhoisResult{
				IsAvailable: nil,
				RawText:     "Some raw text from WHOIS server",
			},
			mockError:      nil,
			expectedStatus: StatusRegistered,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockWhoisProvider := NewMockWhoisProvider()
			mockWhoisProvider.SetResponse(tc.domain, tc.mockResult, tc.mockError)

			status := lookupWithWhois(mockWhoisProvider, tc.domain)
			if status != tc.expectedStatus {
				t.Errorf("lookupWithWhois(%q) = %q, want %q", tc.domain, status, tc.expectedStatus)
			}
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}

func TestLookupDomain(t *testing.T) {
	tests := []struct {
		name             string
		domain           string
		mockRDAPResponse *rdap.Response
		mockRDAPError    error
		mockWhoisResult  *WhoisResult
		mockWhoisError   error
		expectedStatus   string
	}{
		{
			name:             "Domain is registered via RDAP",
			domain:           "example.com",
			mockRDAPResponse: MockResponse("example.com"),
			mockRDAPError:    nil,
			// WHOIS shouldn't be called when RDAP succeeds, but provide a mock anyway
			mockWhoisResult: nil,
			mockWhoisError:  nil,
			expectedStatus:  StatusRegistered,
		},
		{
			name:             "Domain not found by RDAP, WHOIS finds available",
			domain:           "nonexistent-domain-for-rdap.com",
			mockRDAPResponse: nil,
			mockRDAPError:    ErrDomainNotFound,
			mockWhoisResult: &WhoisResult{
				IsAvailable: boolPtr(true),
				RawText:     "",
			},
			mockWhoisError: nil,
			expectedStatus: StatusAvailable,
		},
		{
			name:             "RDAP Error, WHOIS finds registered",
			domain:           "google.com",
			mockRDAPResponse: nil,
			mockRDAPError:    errors.New("connection timeout"),
			mockWhoisResult: &WhoisResult{
				IsAvailable: boolPtr(false),
				RawText:     "Domain Name: google.com",
			},
			mockWhoisError: nil,
			expectedStatus: StatusRegistered,
		},
		{
			name:             "RDAP Error, WHOIS also fails",
			domain:           "some.invalidtld",
			mockRDAPResponse: nil,
			mockRDAPError:    errors.New("some RDAP error"),
			mockWhoisResult:  nil,
			mockWhoisError:   errors.New("whois query failed"),
			expectedStatus:   StatusUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockRDAPClient := NewMockRDAPClient()
			mockRDAPClient.SetResponse(tc.domain, tc.mockRDAPResponse, tc.mockRDAPError)

			mockWhoisProvider := NewMockWhoisProvider()
			mockWhoisProvider.SetResponse(tc.domain, tc.mockWhoisResult, tc.mockWhoisError)

			status := lookupDomain(mockRDAPClient, mockWhoisProvider, tc.domain)
			if status != tc.expectedStatus {
				t.Errorf("lookupDomain(%q) with RDAP error '%v' = %q, want %q", tc.domain, tc.mockRDAPError, status, tc.expectedStatus)
			}
		})
	}
}

func TestLookupDomainMCP(t *testing.T) {
	tests := []struct {
		name             string
		domain           string
		mockRDAPResponse *rdap.Response
		mockRDAPError    error
		mockWhoisResult  *WhoisResult
		mockWhoisError   error
		expectedJSON     string
	}{
		{
			name:             "Registered domain via MCP (RDAP success)",
			domain:           "example.com",
			mockRDAPResponse: MockResponse("example.com"),
			mockRDAPError:    nil,
			mockWhoisResult:  nil, // WHOIS not called when RDAP succeeds
			mockWhoisError:   nil,
			expectedJSON:     `{"example.com":"registered"}`,
		},
		{
			name:             "Domain via MCP (RDAP fails, WHOIS finds available)",
			domain:           "nonexistent-domain-mcp.com",
			mockRDAPResponse: nil,
			mockRDAPError:    ErrDomainNotFound,
			mockWhoisResult: &WhoisResult{
				IsAvailable: boolPtr(true),
				RawText:     "",
			},
			mockWhoisError: nil,
			expectedJSON:   `{"nonexistent-domain-mcp.com":"available"}`,
		},
		{
			name:             "Domain via MCP (RDAP fails, WHOIS finds registered)",
			domain:           "google.com",
			mockRDAPResponse: nil,
			mockRDAPError:    errors.New("connection timeout"),
			mockWhoisResult: &WhoisResult{
				IsAvailable: boolPtr(false),
				RawText:     "Domain Name: google.com",
			},
			mockWhoisError: nil,
			expectedJSON:   `{"google.com":"registered"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockRDAPClient := NewMockRDAPClient()
			mockRDAPClient.SetResponse(tc.domain, tc.mockRDAPResponse, tc.mockRDAPError)

			mockWhoisProvider := NewMockWhoisProvider()
			mockWhoisProvider.SetResponse(tc.domain, tc.mockWhoisResult, tc.mockWhoisError)

			args := SingleDomainLookup{Domain: tc.domain}

			resp, err := lookupDomainMCP(mockRDAPClient, mockWhoisProvider, args)

			if err != nil {
				t.Errorf("lookupDomainMCP() error = %v, want nil", err)
			}
			if resp == nil {
				t.Fatalf("lookupDomainMCP() resp is nil")
			}
			if len(resp.Content) == 0 {
				t.Fatalf("lookupDomainMCP() response has no content")
			}

			textContent := resp.Content[0].TextContent.Text

			if textContent != tc.expectedJSON {
				t.Errorf("lookupDomainMCP() result JSON = %q, want %q", textContent, tc.expectedJSON)
			}
		})
	}
}

func TestLookupDomainsMCP(t *testing.T) {
	domains := []string{"example.com", "nonexistent-domain-mcp-multi.com", "google.com"}

	mockRDAPClient := NewMockRDAPClient()
	// RDAP succeeds for example.com
	mockRDAPClient.SetResponse("example.com", MockResponse("example.com"), nil)
	// RDAP fails for the others, triggering WHOIS fallback
	mockRDAPClient.SetResponse("nonexistent-domain-mcp-multi.com", nil, ErrDomainNotFound)
	mockRDAPClient.SetResponse("google.com", nil, errors.New("some rdap error"))

	mockWhoisProvider := NewMockWhoisProvider()
	// Set up the WHOIS responses
	mockWhoisProvider.SetResponse("nonexistent-domain-mcp-multi.com", &WhoisResult{
		IsAvailable: boolPtr(true),
		RawText:     "",
	}, nil)
	mockWhoisProvider.SetResponse("google.com", &WhoisResult{
		IsAvailable: boolPtr(false),
		RawText:     "Domain Name: google.com",
	}, nil)
	// No need to set up example.com because RDAP will succeed

	args := MultipleDomainsLookup{Domains: domains}
	// Call MCP handler with both mock clients
	resp, err := lookupDomainsMCP(mockRDAPClient, mockWhoisProvider, args)

	if err != nil {
		t.Errorf("lookupDomainsMCP() error = %v, want nil", err)
	}
	if resp == nil {
		t.Fatalf("lookupDomainsMCP() resp is nil")
	}
	if len(resp.Content) == 0 {
		t.Fatalf("lookupDomainsMCP() response has no content")
	}

	textContent := resp.Content[0].TextContent.Text
	var result map[string]string
	err = json.Unmarshal([]byte(textContent), &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v. Response was: %s", err, textContent)
	}

	// Define expected results based on RDAP mock and WHOIS mock behavior
	expected := map[string]string{
		"example.com":                      StatusRegistered, // Found by RDAP mock
		"nonexistent-domain-mcp-multi.com": StatusAvailable,  // RDAP fails, mock WHOIS finds available
		"google.com":                       StatusRegistered, // RDAP fails, mock WHOIS finds registered
	}

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("lookupDomainsMCP() result = %v, want %v", result, expected)
	}
}

func TestEmptyDomainsLookup(t *testing.T) {
	mockRDAPClient := NewMockRDAPClient()
	mockWhoisProvider := NewMockWhoisProvider()
	args := MultipleDomainsLookup{Domains: []string{}}

	// Pass both mock clients
	resp, err := lookupDomainsMCP(mockRDAPClient, mockWhoisProvider, args)

	if err != nil {
		t.Errorf("lookupDomainsMCP() with empty domains error = %v, want nil", err)
	}
	if resp == nil {
		t.Fatalf("lookupDomainsMCP() resp is nil")
	}

	if len(resp.Content) == 0 {
		t.Fatalf("lookupDomainsMCP() response has no content")
	}

	textContent := resp.Content[0].TextContent.Text
	if textContent != "{}" { // Expect exactly "{}" for empty input
		t.Errorf("Expected empty JSON object '{}', got %q", textContent)
	}

	// Optional: Keep the unmarshal check for robustness
	var result map[string]string
	err = json.Unmarshal([]byte(textContent), &result)
	if err != nil {
		t.Errorf("Failed to unmarshal response: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("Expected empty result map for empty domains list, got %v", result)
	}
}

func TestMinWorkers(t *testing.T) {
	tests := []struct {
		name     string
		a        int
		b        int
		expected int
	}{
		{"a less than b", 5, 10, 5},
		{"a equal to b", 7, 7, 7},
		{"a greater than b", 10, 3, 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := minWorkers(tc.a, tc.b)
			if result != tc.expected {
				t.Errorf("minWorkers(%d, %d) = %d, want %d", tc.a, tc.b, result, tc.expected)
			}
		})
	}
}
