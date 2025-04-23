package main

import (
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

func TestLookupDomain(t *testing.T) {
	tests := []struct {
		name           string
		domain         string
		mockResponse   *rdap.Response
		mockError      error
		expectedStatus string
	}{
		{
			name:           "Domain is registered",
			domain:         "example.com",
			mockResponse:   MockResponse("example.com"),
			mockError:      nil,
			expectedStatus: "registered",
		},
		{
			name:           "Domain is available",
			domain:         "nonexistent-domain.com",
			mockResponse:   nil,
			mockError:      ErrDomainNotFound,
			expectedStatus: "available",
		},
		{
			name:           "Error during lookup",
			domain:         "error-domain.com",
			mockResponse:   nil,
			mockError:      errors.New("connection timeout"),
			expectedStatus: "available",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := NewMockRDAPClient()
			mockClient.SetResponse(tc.domain, tc.mockResponse, tc.mockError)

			status := lookupDomain(mockClient, tc.domain)
			if status != tc.expectedStatus {
				t.Errorf("lookupDomain() = %v, want %v", status, tc.expectedStatus)
			}
		})
	}
}

func TestLookupDomainMCP(t *testing.T) {
	tests := []struct {
		name           string
		domain         string
		mockResponse   *rdap.Response
		mockError      error
		expectedStatus string
	}{
		{
			name:           "Registered domain via MCP",
			domain:         "example.com",
			mockResponse:   MockResponse("example.com"),
			mockError:      nil,
			expectedStatus: "registered",
		},
		{
			name:           "Available domain via MCP",
			domain:         "available-domain.com",
			mockResponse:   nil,
			mockError:      ErrDomainNotFound,
			expectedStatus: "available",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := NewMockRDAPClient()
			mockClient.SetResponse(tc.domain, tc.mockResponse, tc.mockError)

			args := SingleDomainLookup{Domain: tc.domain}
			resp, err := lookupDomainMCP(mockClient, args)

			if err != nil {
				t.Errorf("lookupDomainMCP() error = %v, want nil", err)
			}
			if resp == nil {
				t.Fatalf("lookupDomainMCP() resp is nil")
			}

			if len(resp.Content) == 0 {
				t.Fatalf("lookupDomainMCP() response has no content")
			}

			// Get the text content from the first content item
			textContent := resp.Content[0].TextContent.Text
			var result map[string]string
			err = json.Unmarshal([]byte(textContent), &result)
			if err != nil {
				t.Errorf("Failed to unmarshal response: %v", err)
			}

			if result[tc.domain] != tc.expectedStatus {
				t.Errorf("lookupDomainMCP() result = %v, want %v", result[tc.domain], tc.expectedStatus)
			}
		})
	}
}

func TestLookupDomainsMCP(t *testing.T) {
	domains := []string{"example.com", "available-domain.com"}

	mockClient := NewMockRDAPClient()
	mockClient.SetResponse("example.com", MockResponse("example.com"), nil)
	mockClient.SetResponse("available-domain.com", nil, ErrDomainNotFound)

	args := MultipleDomainsLookup{Domains: domains}
	resp, err := lookupDomainsMCP(mockClient, args)

	if err != nil {
		t.Errorf("lookupDomainsMCP() error = %v, want nil", err)
	}
	if resp == nil {
		t.Fatalf("lookupDomainsMCP() resp is nil")
	}

	if len(resp.Content) == 0 {
		t.Fatalf("lookupDomainsMCP() response has no content")
	}

	// Get the text content from the first content item
	textContent := resp.Content[0].TextContent.Text
	var result map[string]string
	err = json.Unmarshal([]byte(textContent), &result)
	if err != nil {
		t.Errorf("Failed to unmarshal response: %v", err)
	}

	expected := map[string]string{
		"example.com":          "registered",
		"available-domain.com": "available",
	}

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("lookupDomainsMCP() = %v, want %v", result, expected)
	}
}

func TestEmptyDomainsLookup(t *testing.T) {
	mockClient := NewMockRDAPClient()
	args := MultipleDomainsLookup{Domains: []string{}}

	resp, err := lookupDomainsMCP(mockClient, args)

	if err != nil {
		t.Errorf("lookupDomainsMCP() with empty domains error = %v, want nil", err)
	}

	if len(resp.Content) == 0 {
		t.Fatalf("lookupDomainsMCP() response has no content")
	}

	// Get the text content from the first content item
	textContent := resp.Content[0].TextContent.Text
	var result map[string]string
	err = json.Unmarshal([]byte(textContent), &result)
	if err != nil {
		t.Errorf("Failed to unmarshal response: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("Expected empty result for empty domains list, got %v", result)
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
