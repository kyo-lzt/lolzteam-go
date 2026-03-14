package main

import (
	"fmt"
	"os"

	"github.com/kyo-lzt/lolzteam-go/internal/codegen"
)

func main() {
	outDir := "."
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}

	apis := []codegen.APIConfig{
		{SchemaFile: "schemas/forum.json", Prefix: "Forum", BaseURL: "https://prod-api.lolz.live", RPM: 300},
		{SchemaFile: "schemas/market.json", Prefix: "Market", BaseURL: "https://prod-api.lzt.market", RPM: 120},
	}

	if err := codegen.Generate(outDir, apis); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
