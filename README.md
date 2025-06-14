# Repojacking Checker

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A tool for detecting potential repository hijacking (repojacking) vulnerabilities in GitHub repositories.

## Components

The project consists of three main components:

1. **BigQuery JSON Parser** (`cmd/bq-json-parser`)
   - Parses BigQuery JSON results
   - Processes repository data for analysis

2. **Repository Checker** (`cmd/repo-checker`)
   - Checks repository availability on GitHub
   - Identifies deleted or moved repositories

2. **Signup Checker** (`cmd/signup-checker`)
   - Verifies if account names are available for registration
   - Helps identify potentially vulnerable repositories

## Installation

```bash
go get github.com/Waramoto/repojacking-checker
```

## Usage

### BigQuery JSON Parser

```bash
go run cmd/bq-json-parser/main.go <input.json>
```

- `<input.json>`: BigQuery results JSON file to parse

### Repository Checker

```bash
go run cmd/repo-checker/main.go repositories.csv <number_of_workers>
```

- `repositories.csv`: CSV file containing repositories data
- `<number_of_workers>`: Number of concurrent workers for processing

### Signup Checker

```bash
go run cmd/signup-checker/main.go corrupted_repos.csv <number_of_workers>
```

- `corrupted_repos.csv`: CSV file containing repositories that are no longer available or have been moved
- `<number_of_workers>`: Number of concurrent workers for processing

## Error Handling

- Implements exponential backoff for rate limiting
- Maximum retry timeout of 5 minutes
- Graceful handling of HTTP timeouts and connection issues
- Progress saving for interrupted scans

## Output Files

- `repositories.csv`: List of repositories extracted from BigQuery JSON results
- `corrupted_repos.csv`: List of repositories that are no longer available or have been moved
- `vulnerable_repos.csv`: List of repositories potentially vulnerable to repojacking
- Progress files are maintained to resume interrupted scans

## License

This project is licensed under the MIT License.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
