package main

import (
	"fmt"
	"log"
	"os"
	"strings"
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

	found := false
	if err == nil && resp != nil {
		if _, ok := resp.Object.(*rdap.Domain); ok {
			found = true
		}
	} else if err != nil {
		log.Printf("RDAP lookup error for %s: %v", domain, err)
	}

	var resultMsg string
	if found {
		resultMsg = fmt.Sprintf("Domain '%s' appears to be registered.", domain)
	} else {
		resultMsg = fmt.Sprintf("Domain '%s' appears to be available or could not be confirmed.", domain)
	}
	return resultMsg
}

func lookupDomainMCP(client *rdap.Client, args SingleDomainLookup) (*mcp.ToolResponse, error) {
	log.Printf("Received lookup request for domain: %s", args.Domain)

	resultMsg := lookupDomain(client, args.Domain)

	log.Printf("Responding: %s", resultMsg)

	return mcp.NewToolResponse(mcp.NewTextContent(resultMsg)), nil
}

func lookupDomainsMCP(client *rdap.Client, args MultipleDomainsLookup) (*mcp.ToolResponse, error) {
	log.Printf("Received lookup request for %d domains: %v", len(args.Domains), args.Domains)

	numDomains := len(args.Domains)
	if numDomains == 0 {
		return mcp.NewToolResponse(mcp.NewTextContent("No domains provided for lookup.")), nil
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
			log.Printf("Worker %d started.", workerID)
			for domain := range tasks {
				log.Printf("Worker %d processing domain: %s", workerID, domain)
				resultMsg := lookupDomain(client, domain)
				singleResult := map[string]string{domain: resultMsg}
				resultsChan <- singleResult
			}
			log.Printf("Worker %d finished.", workerID)
		}(i)
	}

	log.Println("Dispatching tasks to workers...")
	for _, domain := range args.Domains {
		tasks <- domain
	}
	close(tasks)
	log.Println("All tasks dispatched. Waiting for workers...")

	workerWg.Wait()
	log.Println("All workers finished.")

	close(resultsChan)
	log.Println("Results channel closed.")

	finalResults := make(map[string]string, numDomains)
	log.Println("Collecting results...")
	for result := range resultsChan {
		for domain, msg := range result {
			finalResults[domain] = msg
		}
	}
	log.Printf("Collected %d results.", len(finalResults))

	var responseBuilder strings.Builder
	responseBuilder.WriteString("Domain lookup results:\n")
	for _, domain := range args.Domains {
		responseBuilder.WriteString(fmt.Sprintf("- %s\n", finalResults[domain]))
	}

	finalResponse := responseBuilder.String()
	log.Printf("Responding with combined results:\n%s", finalResponse)

	return mcp.NewToolResponse(mcp.NewTextContent(finalResponse)), nil
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
		"Looks up a single domain name using RDAP to check if it is registered.",
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
		"Looks up multiple domain names using RDAP to check if they are registered.",
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
