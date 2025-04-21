package main

import (
	"fmt"
	"os"

	"github.com/openrdap/rdap"
)

func lookupDomain(domain string) bool {
	// Create a new RDAP client
	client := &rdap.Client{}

	// Create the RDAP request for a domain object.
	// Assuming NewRequest doesn't return an error based on linter feedback.
	req := rdap.NewRequest(rdap.DomainRequest, domain)

	resp, err := client.Do(req)

	if err != nil {
		return false
	}

	if resp != nil {
		if _, ok := resp.Object.(*rdap.Domain); ok {
			return true
		}
	}

	return false
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <domain_name>")
		os.Exit(1)
	}
	domainName := os.Args[1]
	result := lookupDomain(domainName)

	if result {
		fmt.Println(domainName, "found")
	} else {
		fmt.Println(domainName, "not found")
	}

}
