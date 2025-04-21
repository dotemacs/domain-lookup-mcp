package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	mcp "github.com/metoro-io/mcp-golang"
	mcp_stdio "github.com/metoro-io/mcp-golang/transport/stdio"
	"github.com/openrdap/rdap"
)

type SingleDomainLookup struct {
	Domain string `json:"domain" jsonschema:"required,description=The domain name to look up (e.g., google.com)"`
}

type MultipleDomainsLookup struct {
	Domains []string `json:"domains" jsonschema:"required,description=A list of domain names to look up (e.g., [\"google.com\", \"example.com\"])"`
}

func lookupDomain(client *rdap.Client, domain string) string {
	log.Printf("Performing RDAP lookup for: %s", domain)
	req := rdap.NewRequest(rdap.DomainRequest, domain)

	resp, err := client.Do(req)

	status := "available"
	if err == nil && resp != nil {
		if _, ok := resp.Object.(*rdap.Domain); ok {
			status = "registered"
		}
	} else if err != nil {
		log.Printf("RDAP lookup error for %s: %v", domain, err)
	}

	log.Printf("RDAP lookup result for %s: %s", domain, status)
	return status
}

func lookupDomainMCP(client *rdap.Client, args SingleDomainLookup) (*mcp.ToolResponse, error) {
	log.Printf("Received single lookup request for domain: %s", args.Domain)

	status := lookupDomain(client, args.Domain)

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

func lookupDomainsMCP(client *rdap.Client, args MultipleDomainsLookup) (*mcp.ToolResponse, error) {
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
				status := lookupDomain(client, domain)
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
		return mcp.NewToolResponse(mcp.NewTextContent(errorMsg)), nil // Or return the error?
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

	server := mcp.NewServer(mcp_stdio.NewStdioServerTransport())

	err := server.RegisterTool(
		"lookup_domain",
		"Looks up a single domain name using RDAP. Returns JSON: {\"domain\": \"status\"} ('registered' or 'available').",
		func(args SingleDomainLookup) (*mcp.ToolResponse, error) {
			return lookupDomainMCP(rdapClient, args)
		},
	)
	if err != nil {
		log.Fatalf("Error registering lookup_domain tool: %v", err)
		os.Exit(1)
	}
	log.Println("lookup_domain tool registered.")

	err = server.RegisterTool(
		"lookup_domains",
		"Looks up multiple domain names using RDAP. Returns JSON: {\"domain1\": \"status1\", ...} ('registered' or 'available').",
		func(args MultipleDomainsLookup) (*mcp.ToolResponse, error) {
			return lookupDomainsMCP(rdapClient, args)
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

	select {}
}
