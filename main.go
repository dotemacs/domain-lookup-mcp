package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	mcp "github.com/metoro-io/mcp-golang"
	mcp_stdio "github.com/metoro-io/mcp-golang/transport/stdio"
	"github.com/openrdap/rdap"
	"github.com/shlin168/go-whois/whois"
)

const (
	StatusRegistered = "registered"
	StatusAvailable  = "available"
	StatusUnknown    = "unknown"
)

type RDAPClient interface {
	Do(req *rdap.Request) (*rdap.Response, error)
}

type WhoisResult struct {
	IsAvailable *bool
	RawText     string
}

type WhoisProvider interface {
	Query(ctx context.Context, domain string) (*WhoisResult, error)
}

type WhoisClient struct {
	client *whois.Client
}

func NewWhoisClient() (WhoisProvider, error) {
	c, err := whois.NewClient()
	if err != nil {
		return nil, fmt.Errorf("error creating real WHOIS client: %w", err)
	}
	return &WhoisClient{client: c}, nil
}

func (g *WhoisClient) Query(ctx context.Context, domain string) (*WhoisResult, error) {
	result, err := g.client.Query(ctx, domain)
	if err != nil {
		return nil, err
	}

	return &WhoisResult{
		IsAvailable: result.IsAvailable,
		RawText:     result.RawText,
	}, nil
}

type SingleDomainLookup struct {
	Domain string `json:"domain" jsonschema:"required,description=The domain name to look up (e.g., foo.bar)"`
}

type MultipleDomainsLookup struct {
	Domains []string `json:"domains" jsonschema:"required,description=A list of domain names to look up (e.g., [\"foo.bar\", \"example.com\"])"`
}

func lookupWithWhois(whoisClient WhoisProvider, domain string) string {
	log.Printf("Performing WHOIS lookup for: %s", domain)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	whoisResult, err := whoisClient.Query(ctx, domain)
	if err != nil {
		log.Printf("WHOIS lookup error for %s: %v", domain, err)
		return StatusUnknown
	}

	status := StatusUnknown
	if whoisResult.IsAvailable != nil {
		if *whoisResult.IsAvailable {
			status = StatusAvailable
		} else {
			status = StatusRegistered
		}
	} else if whoisResult.RawText != "" {
		log.Printf("WHOIS raw text found for %s but availability unclear, inferring as registered", domain)
		status = StatusRegistered
	}

	log.Printf("WHOIS lookup result for %s: %s", domain, status)
	return status
}

func lookupDomain(rdapClient RDAPClient, whoisClient WhoisProvider, domain string) string {
	log.Printf("Performing RDAP lookup for: %s", domain)
	req := rdap.NewRequest(rdap.DomainRequest, domain)

	resp, err := rdapClient.Do(req)

	status := StatusUnknown

	if err != nil {
		log.Printf("RDAP lookup error for %s: %v", domain, err)
	} else if resp != nil {
		if _, ok := (*resp).Object.(*rdap.Domain); ok {
			status = StatusRegistered
		} else {
			log.Printf("RDAP lookup for %s succeeded but response object was not *rdap.Domain", domain)
		}
	}

	log.Printf("RDAP lookup intermediate result for %s: %s", domain, status)

	if status == StatusUnknown {
		log.Printf("RDAP status for %s is unknown, attempting WHOIS fallback lookup.", domain)
		status = lookupWithWhois(whoisClient, domain)
	}

	log.Printf("Final lookup result for %s: %s", domain, status)
	return status
}

func lookupDomainMCP(rdapClient RDAPClient, whoisClient WhoisProvider, args SingleDomainLookup) (*mcp.ToolResponse, error) {
	log.Printf("Received single lookup request for domain: %s", args.Domain)

	status := lookupDomain(rdapClient, whoisClient, args.Domain)

	resultMap := map[string]string{
		args.Domain: status,
	}

	jsonBytes, err := json.Marshal(resultMap)
	if err != nil {
		log.Printf("Error marshalling single domain result to JSON: %v", err)
		errorMsg := fmt.Sprintf("Error formatting result for %s", args.Domain)
		return mcp.NewToolResponse(mcp.NewTextContent(errorMsg)), nil
	}

	jsonString := string(jsonBytes)
	log.Printf("Responding with JSON string: %s", jsonString)

	return mcp.NewToolResponse(mcp.NewTextContent(jsonString)), nil
}

func lookupDomainsMCP(rdapClient RDAPClient, whoisClient WhoisProvider, args MultipleDomainsLookup) (*mcp.ToolResponse, error) {
	log.Printf("Received multiple lookup request for %d domains: %v", len(args.Domains), args.Domains)

	numDomains := len(args.Domains)
	if numDomains == 0 {
		return mcp.NewToolResponse(mcp.NewTextContent("{}")), nil
	}

	const numWorkers = 10

	tasks := make(chan string, numDomains)
	resultsChan := make(chan map[string]string, numDomains)

	var workerWg sync.WaitGroup

	actualWorkers := minWorkers(numWorkers, numDomains)
	log.Printf("Starting %d workers for %d domains.", actualWorkers, numDomains)
	for i := 0; i < actualWorkers; i++ {
		workerWg.Add(1)
		go func(workerID int) {
			defer workerWg.Done()
			for domain := range tasks {
				status := lookupDomain(rdapClient, whoisClient, domain)
				singleResult := map[string]string{domain: status}
				resultsChan <- singleResult
			}
		}(i)
	}

	for _, domain := range args.Domains {
		tasks <- domain
	}
	close(tasks)

	workerWg.Wait()
	close(resultsChan)

	finalResults := make(map[string]string, numDomains)
	for result := range resultsChan {
		for domain, status := range result {
			finalResults[domain] = status
		}
	}

	jsonBytes, err := json.Marshal(finalResults)
	if err != nil {
		log.Printf("Error marshalling multiple domain results to JSON: %v", err)
		errorMsg := "Error formatting results for multiple domains"
		return mcp.NewToolResponse(mcp.NewTextContent(errorMsg)), nil
	}

	jsonString := string(jsonBytes)
	log.Printf("Responding with JSON string: %s", jsonString)

	return mcp.NewToolResponse(mcp.NewTextContent(jsonString)), nil
}

func minWorkers(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	log.Println("Starting MCP Server via stdio...")

	rdapClient := &rdap.Client{}
	log.Println("Shared RDAP client created.")

	whoisClient, err := NewWhoisClient()
	if err != nil {
		log.Fatalf("Error creating WHOIS client: %v", err)
		os.Exit(1)
	}
	log.Println("Shared WHOIS client created.")

	server := mcp.NewServer(mcp_stdio.NewStdioServerTransport())

	err = server.RegisterTool(
		"lookup_domain",
		`Looks up a single domain name using RDAP (with WHOIS fallback). Returns JSON: {"domain": "status"} ('registered', 'available', or 'unknown')`,
		func(args SingleDomainLookup) (*mcp.ToolResponse, error) {
			return lookupDomainMCP(rdapClient, whoisClient, args)
		},
	)
	if err != nil {
		log.Fatalf("Error registering lookup_domain tool: %v", err)
		os.Exit(1)
	}
	log.Println("lookup_domain tool registered.")

	err = server.RegisterTool(
		"lookup_domains",
		`Looks up multiple domain names using RDAP (with WHOIS fallback). Returns JSON: {"domain1": "status1", ...} ('registered', 'available', or 'unknown')`,
		func(args MultipleDomainsLookup) (*mcp.ToolResponse, error) {
			return lookupDomainsMCP(rdapClient, whoisClient, args)
		},
	)
	if err != nil {
		log.Fatalf("Error registering lookup_domains tool: %v", err)
		os.Exit(1)
	}
	log.Println("lookup_domains tool registered. MCP Server waiting for requests...")

	err = server.Serve()
	if err != nil {
		log.Fatalf("MCP Server error: %v", err)
	}

	select {} // Keep server running
}
