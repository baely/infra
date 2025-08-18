// Scout tracks all repositories for a user and pulls down deploy config into the appropriate location.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/google/go-github/v74/github"
)

const (
	githubUser = "baely"
	deployDir  = "config"
)

var (
	githubToken = os.Getenv("GITHUB_TOKEN")
	requiredDirs = []string{"docker"}
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Move out of the go directory
	if err := os.Chdir(path.Dir(must(os.Getwd()))); err != nil {
		return fmt.Errorf("failed to change directory: %w", err)
	}

	ctx := context.Background()
	return fetchDeployConfigs(ctx)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// fetchDeployConfigs finds all GitHub repositories that contain deploy config.
func fetchDeployConfigs(ctx context.Context) error {
	if githubToken == "" {
		return fmt.Errorf("GITHUB_TOKEN environment variable is required")
	}

	// Ensure required directories exist
	for _, dir := range requiredDirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	client := github.NewClient(nil).WithAuthToken(githubToken)

	var allRepositories []*github.Repository
	opts := &github.RepositoryListByAuthenticatedUserOptions{
		ListOptions: github.ListOptions{
			Page:    0,
			PerPage: 100,
		},
	}

	for {
		repositories, resp, err := client.Repositories.ListByAuthenticatedUser(ctx, opts)
		if err != nil {
			return fmt.Errorf("failed to list repos: %w", err)
		}

		allRepositories = append(allRepositories, repositories...)

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	for _, repository := range allRepositories {
		if *repository.Owner.Login != githubUser {
			continue
		}

		_, dirContent, _, err := client.Repositories.GetContents(ctx, *repository.Owner.Login, *repository.Name, deployDir, nil)
		if err != nil {
			// Skip repositories without deploy config
			continue
		}

		if len(dirContent) == 0 {
			continue
		}

		displayURL := repository.GetHTMLURL()
		displayURL = strings.TrimPrefix(displayURL, "https://")
		displayURL = strings.ReplaceAll(displayURL, "/", "_")

		// Create directory if required
		dir := path.Join("docker", displayURL)
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create directory %s: %v\n", dir, err)
			continue
		}

		// Get the latest commit SHA for the repository
		latestCommit, _, err := client.Repositories.GetCommit(ctx, *repository.Owner.Login, *repository.Name, "HEAD", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get latest commit for %s: %v\n", *repository.Name, err)
			continue
		}
		latestSHA := latestCommit.GetSHA()

		for _, content := range dirContent {
			rawURL := content.GetDownloadURL()
			if rawURL == "" {
				continue
			}

			filename := path.Join(dir, content.GetName())
			if err := downloadFile(rawURL, filename); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to download %s: %v\n", content.GetName(), err)
				continue
			}

			// Replace SHA placeholders in the downloaded file
			if err := replaceShaPlaceholders(filename, latestSHA); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to replace SHA placeholders in %s: %v\n", content.GetName(), err)
			}
		}
	}

	return nil
}

func downloadFile(url, filename string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	return os.WriteFile(filename, b, 0644)
}

func replaceShaPlaceholders(filename, sha string) error {
	content, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	updatedContent := strings.ReplaceAll(string(content), "{{sha}}", sha)

	if err := os.WriteFile(filename, []byte(updatedContent), 0644); err != nil {
		return fmt.Errorf("failed to write updated file: %w", err)
	}

	return nil
}
