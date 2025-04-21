package main

import (
	"fmt"
	"log"
	"os"

	mcp "github.com/metoro-io/mcp-golang"
	mcp_stdio "github.com/metoro-io/mcp-golang/transport/stdio"
	"github.com/openrdap/rdap"
)

// DomainLookupArgs defines the arguments for the domain lookup tool.
type DomainLookupArgs struct {
	Domain string `json:"domain" jsonschema:"required,description=The domain name to look up (e.g., google.com)"`
}

// lookupDomainMCP is the MCP tool implementation for domain lookup.
// It takes DomainLookupArgs and returns an MCP ToolResponse.
func lookupDomainMCP(args DomainLookupArgs) (*mcp.ToolResponse, error) {
	log.Printf("Received lookup request for domain: %s", args.Domain)

	// Create a new RDAP client
	client := &rdap.Client{}

	// Create the RDAP request for a domain object.
	req := rdap.NewRequest(rdap.DomainRequest, args.Domain)

	// Perform the RDAP query
	resp, err := client.Do(req)

	found := false
	if err == nil && resp != nil {
		if _, ok := resp.Object.(*rdap.Domain); ok {
			found = true
		}
	} else if err != nil {
		// Log RDAP specific errors, but don't return them to the MCP client unless critical.
		// The primary result is whether it was found or not.
		log.Printf("RDAP lookup error for %s: %v", args.Domain, err)
		// Consider if specific RDAP errors should result in an error response.
		// For now, any RDAP error implies 'not found' for simplicity.
	}

	// Construct the response message
	var resultMsg string
	if found {
		resultMsg = fmt.Sprintf("Domain '%s' appears to be registered.", args.Domain)
	} else {
		// We infer 'not found' if RDAP errored or didn't return a Domain object.
		resultMsg = fmt.Sprintf("Domain '%s' appears to be available or could not be confirmed.", args.Domain)
	}

	log.Printf("Responding: %s", resultMsg)

	// Return the result as an MCP text content response.
	return mcp.NewToolResponse(mcp.NewTextContent(resultMsg)), nil
}

func main() {
	log.Println("Starting MCP Server via stdio...")

	// Initialize MCP server with stdio transport
	server := mcp.NewServer(mcp_stdio.NewStdioServerTransport())

	// Register the domain lookup tool
	err := server.RegisterTool(
		"lookup_domain",
		"Looks up a domain name using RDAP to check if it is registered.",
		lookupDomainMCP, // Pass the function reference
	)
	if err != nil {
		log.Fatalf("Error registering lookup_domain tool: %v", err)
		os.Exit(1)
	}

	log.Println("lookup_domain tool registered. MCP Server waiting for requests...")

	// Start the server - this will block and handle requests via stdio
	err = server.Serve()
	if err != nil {
		log.Fatalf("MCP Server error: %v", err)
	}

	select {}
}
