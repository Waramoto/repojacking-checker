package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type RepoData struct {
	RepoName []string `json:"repo_name"`
}

func main() {
	if len(os.Args) <= 1 {
		fmt.Println("Please provide JSON file path")
		return
	}

	inputFile := os.Args[1]
	if filepath.Ext(inputFile) != ".json" {
		fmt.Println("Provided file is not JSON")
		return
	}

	outputFile := "repositories.csv"

	input, err := os.Open(inputFile)
	if err != nil {
		fmt.Printf("Error opening input file: %v\n", err)
		return
	}
	defer input.Close()

	output, err := os.OpenFile(outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Error opening output file: %v\n", err)
		return
	}
	defer output.Close()

	writer := csv.NewWriter(output)
	defer writer.Flush()

	reader := bufio.NewReader(input)

	uniqueMap := make(map[string]struct{})

	lineNumber := 0

	fmt.Printf("Parsing file %s...\n", inputFile)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			fmt.Printf("Error reading input file: %v\n", err)
			return
		}

		lineNumber++

		var data RepoData
		if err = json.Unmarshal([]byte(line), &data); err != nil {
			fmt.Printf("Error parsing JSON on line %d: %v\n", lineNumber, err)
			continue
		}

		for _, repo := range data.RepoName {
			if _, ok := uniqueMap[repo]; ok {
				continue
			}
			uniqueMap[repo] = struct{}{}

			parts := strings.Split(repo, "/")
			if len(parts) != 2 {
				fmt.Printf("Invalid repo format on line %d: %s\n", lineNumber, repo)
				continue
			}

			if err = writer.Write(parts); err != nil {
				fmt.Printf("Error writing to CSV on line %d: %v\n", lineNumber, err)
				return
			}
		}

		fmt.Printf("\rProcessed lines: %d", lineNumber)
	}

	fmt.Printf("\nSuccessfully parsed %d unique repositories\n", len(uniqueMap))
}
