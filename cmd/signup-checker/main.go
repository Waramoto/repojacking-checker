package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var ErrTooManyRequests = errors.New("too many requests")

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalChan
		fmt.Println("\nReceived shutdown signal. Cleaning up...")
		cancel()
	}()

	if len(os.Args) <= 2 {
		fmt.Println("Please provide CSV file and number of workers")
		return
	}

	inputFile := os.Args[1]
	if filepath.Ext(inputFile) != ".csv" {
		fmt.Println("Provided file is not CSV")
		return
	}

	workersNum, err := strconv.Atoi(os.Args[2])
	if err != nil {
		fmt.Println("Provided number of workers is incorrect")
		return
	}

	file, err := os.Open(inputFile)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		fmt.Printf("Error reading CSV file: %v\n", err)
		return
	}

	outputFileName := "vulnerable_repos.csv"
	outputFile, err := os.OpenFile(outputFileName, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		fmt.Printf("Error creating/opening output file: %v\n", err)
		return
	}
	defer outputFile.Close()

	vulnerableReposNum, err := getVulnerableReposNum(outputFile)
	if err != nil {
		fmt.Printf("WARN: failed to get vulnerable repositories number: %v\n", err)
	}

	progressFileName := "signup_checker_progress.txt"
	processedLines, err := getProcessedLines(progressFileName)
	if err != nil {
		fmt.Printf("WARN: failed to get processed lines: %v\n", err)
	}

	writer := csv.NewWriter(outputFile)
	defer writer.Flush()

	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}

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
					fmt.Printf("Error writing to CSV: %v\n", err)
				}

				vulnerableReposNum++
			case <-ctx.Done():
				return
			}
		}
	}()

	fmt.Printf("Processing file %s with %d worker(s)...\n", inputFile, workersNum)

	sem := make(chan struct{}, workersNum)
	var workerWG sync.WaitGroup

loop:
	for i := processedLines; i < len(records); i++ {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}

		record := records[i]

		if len(record) < 2 {
			fmt.Printf("WARN: skipping invalid record on line %d: %v\n", i+1, record)
			continue
		}

		account, repo, _ := record[0], record[1], record[2]

		workerWG.Add(1)
		go func(account, repo string) {
			defer workerWG.Done()
			defer func() { <-sem }()

			sleepTime := 5 * time.Second
			const maxSleepTime = 5 * time.Minute

			err = checkSignup(ctx, httpClient, account, repo, results)
			for err != nil {
				if !(errors.Is(err, ErrTooManyRequests) || errors.Is(err, context.DeadlineExceeded)) {
					if !errors.Is(err, context.Canceled) {
						fmt.Printf("\nError checking signup for account %s: %v\n", account, err)
					}
					return
				}

				sleepTime *= 2
				if sleepTime > maxSleepTime {
					sleepTime = maxSleepTime
				}
				time.Sleep(sleepTime)

				err = checkSignup(ctx, httpClient, account, repo, results)
			}
		}(account, repo)

		if err = saveProgress(progressFileName, i+1); err != nil {
			fmt.Printf("WARN: failed to save progress on line %d: %v\n", i+1, record)
		}

		fmt.Printf("\rProcessed records: %d/%d", i+1, len(records))
		processedLines = i + 1
	}

	go func() {
		workerWG.Wait()
		close(results)
	}()

	writerWG.Wait()

	fmt.Printf("\nFounded %d from %d vulnerable repositories\n", vulnerableReposNum, processedLines)
}

func checkSignup(ctx context.Context, client *http.Client, account, repo string, results chan<- []string) error {
	link := getSignupLink(account)

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, link, http.NoBody)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotModified {
		results <- []string{account, repo}
		return nil
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return ErrTooManyRequests
	}

	if resp.StatusCode != http.StatusUnprocessableEntity {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

func getSignupLink(account string) string {
	return fmt.Sprintf("https://github.com/signup_check/username?value=%s", url.QueryEscape(account))
}

func getProcessedLines(progressFile string) (int, error) {
	file, err := os.Open(progressFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("file %s does not exist", progressFile)
		}
		return 0, fmt.Errorf("error opening progress file: %v\n", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		var lineNumber int
		_, err = fmt.Sscanf(scanner.Text(), "%d", &lineNumber)
		if err != nil {
			return 0, fmt.Errorf("error scanning line %d: %v\n", lineNumber, err)
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

func getVulnerableReposNum(outputFile *os.File) (int, error) {
	reader := csv.NewReader(outputFile)
	records, err := reader.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("error reading CSV file: %v\n", err)
	}
	return len(records), nil
}
