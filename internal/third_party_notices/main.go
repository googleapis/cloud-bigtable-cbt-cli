package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"log"

	"golang.org/x/mod/modfile"
)

var firstPartyDomains = []string{
	"cloud.google.com",
	"google.golang.org",
	"github.com/google",
	"github.com/googleapis",
	"golang.org/x",
	"cel.dev",
}

func is1P(m string) bool {
	for _, domain := range firstPartyDomains {
		if strings.HasPrefix(m, domain) {
			return true
		}
	}
	return false
}

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: go run main.go <path to go.mod>")
	}
	goModPath := os.Args[1]

	data, err := os.ReadFile(goModPath)
	if err != nil {
		log.Fatalf("Error reading %s: %v\n", goModPath, err)
	}

	f, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		log.Fatalf("Error parsing %s: %v\n", goModPath, err)
	}

	var modules []string
	for _, req := range f.Require {
		if is1P(req.Mod.Path) {
			modules = append(modules, req.Mod.Path)
		}
	}
	sort.Strings(modules)
	for _, mod := range modules {
		fmt.Println(mod)
	}
}
