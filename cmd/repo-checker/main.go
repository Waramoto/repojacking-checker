package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/go-github/v85/github"
	"github.com/joho/godotenv"
)

type config struct {
	GHToken string `env:"GH_TOKEN"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	err := godotenv.Load()
	if err != nil {
		return fmt.Errorf("failed to load .env file: %w", err)
	}

	ghToken := os.Getenv("GH_TOKEN")
	if ghToken == "" {
		return fmt.Errorf("GH_TOKEN environment variable is not set")
	}

	if len(os.Args) <= 2 {
		return fmt.Errorf("please provide CSV file and number of workers")
	}

	inputFile := os.Args[1]
	if filepath.Ext(inputFile) != ".csv" {
		return fmt.Errorf("provided file is not CSV")
	}

	workersNum, err := strconv.Atoi(os.Args[2])
	if err != nil {
		return fmt.Errorf("provided number of workers is incorrect: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalChan
		fmt.Fprintln(os.Stderr, "\nReceived shutdown signal. Cleaning up...")
		cancel()
	}()

	file, err := os.Open(inputFile)
	if err != nil {
		return fmt.Errorf("error opening file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return fmt.Errorf("error reading CSV file: %w", err)
	}

	outputFileName := "corrupted_repos.csv"
	outputFile, err := os.OpenFile(outputFileName, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("error creating/opening output file: %w", err)
	}
	defer outputFile.Close()

	corruptedLinksNum, err := getCorruptedLinksNum(outputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: failed to get corrupted links number: %v\n", err)
	}

	progressFileName := "repo_checker_progress.txt"
	processedLines, err := getProcessedLines(progressFileName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: failed to get processed lines: %v\n", err)
	}

	writer := csv.NewWriter(outputFile)
	defer writer.Flush()

	results := make(chan []string, workersNum)

	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		for {
			select {
			case result, ok := <-results:
				if !ok {
					return
				}

				if err := writer.Write(result); err != nil {
					fmt.Fprintf(os.Stderr, "Error writing to CSV: %v\n", err)
				}

				corruptedLinksNum++
			case <-ctx.Done():
				return
			}
		}
	}()

	fmt.Printf("Processing file %s with %d worker(s)...\n", inputFile, workersNum)

	sem := make(chan struct{}, workersNum)
	var workerWG sync.WaitGroup

	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}
	githubClient := github.NewClient(httpClient).WithAuthToken(ghToken)

loop:
	for i := processedLines; i < len(records); i++ {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}

		record := records[i]

		if len(record) < 2 {
			fmt.Fprintf(os.Stderr, "WARN: skipping invalid record on line %d: %v\n", i+1, record)
			continue
		}

		owner, repo := record[0], record[1]

		workerWG.Add(1)
		go func(owner, repo string) {
			defer workerWG.Done()
			defer func() { <-sem }()

			sleepTime := 5 * time.Second
			const maxSleepTime = 5 * time.Minute

			err := checkRepo(ctx, githubClient, owner, repo, results)
			for err != nil {
				if !(strings.Contains(err.Error(), "timeout") || errors.Is(err, context.DeadlineExceeded)) {
					if !errors.Is(err, context.Canceled) {
						fmt.Fprintf(os.Stderr, "\nError checking repository %s: %v\n", getRepoLink(owner, repo), err)
					}
					return
				}

				sleepTime *= 2
				if sleepTime > maxSleepTime {
					sleepTime = maxSleepTime
				}
				time.Sleep(sleepTime)

				err = checkRepo(ctx, githubClient, owner, repo, results)
			}
		}(owner, repo)

		if err := saveProgress(progressFileName, i+1); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: failed to save progress on line %d: %v\n", i+1, record)
		}

		fmt.Printf("\rProcessed records: %d/%d", i+1, len(records))
		processedLines = i + 1
	}

	go func() {
		workerWG.Wait()
		close(results)
	}()

	writerWG.Wait()

	fmt.Printf("\nFounded %d corrupted links from %d repositories\n", corruptedLinksNum, processedLines)
	return nil
}

func checkRepo(ctx context.Context, client *github.Client, owner, repo string, results chan<- []string) error {
	_, resp, err := client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMovedPermanently) {
			results <- []string{owner, repo, fmt.Sprintf("%d", resp.StatusCode)}
			return nil
		}
		return fmt.Errorf("error checking repository: %w", err)
	}

	return nil
}

func getRepoLink(owner, repo string) string {
	return fmt.Sprintf("https://github.com/%s/%s", owner, repo)
}

func getProcessedLines(progressFile string) (int, error) {
	file, err := os.Open(progressFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("file %s does not exist", progressFile)
		}
		return 0, fmt.Errorf("error opening progress file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		var lineNumber int
		_, err = fmt.Sscanf(scanner.Text(), "%d", &lineNumber)
		if err != nil {
			return 0, fmt.Errorf("error scanning line %d: %v", lineNumber, err)
		}
		return lineNumber, nil
	}

	return 0, nil
}

func saveProgress(progressFile string, lineNumber int) error {
	file, err := os.Create(progressFile)
	if err != nil {
		return fmt.Errorf("error creating progress file: %w", err)
	}
	defer file.Close()

	_, err = file.WriteString(fmt.Sprintf("%d\n", lineNumber))
	if err != nil {
		return fmt.Errorf("error writing to progress file: %w", err)
	}

	return nil
}

func getCorruptedLinksNum(outputFile *os.File) (int, error) {
	reader := csv.NewReader(outputFile)
	records, err := reader.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("error reading CSV file: %v", err)
	}
	return len(records), nil
}
