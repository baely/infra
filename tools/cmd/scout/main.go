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
	"github.com/spf13/cobra"
)

const (
	githubUser = "baely"
	deployDir  = "config"
)

var (
	githubToken  = os.Getenv("GITHUB_TOKEN")
	requiredDirs = []string{"docker"}
)

var rootCmd = &cobra.Command{
	Use:   "scout",
	Short: "Scout tracks all repositories for a user and pulls down deploy config",
	Long:  "Scout tracks all repositories for a user and pulls down deploy config into the appropriate location.",
}

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan all repositories",
	Long:  "Scan all repositories for deploy config and download them",
	RunE: func(cmd *cobra.Command, args []string) error {
		return run()
	},
}

var repoCmd = &cobra.Command{
	Use:   "repo <name> [ref]",
	Short: "Scan specific repository",
	Long:  "Scan a specific repository at a given ref (default: HEAD)",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo := args[0]
		ref := "HEAD"
		if len(args) >= 2 {
			ref = args[1]
		}
		return runSingleRepo(repo, ref)
	},
}

func main() {
	rootCmd.AddCommand(scanCmd)
	rootCmd.AddCommand(repoCmd)

	if err := rootCmd.Execute(); err != nil {
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
	opts := &github.RepositoryListByUserOptions{
		ListOptions: github.ListOptions{
			Page:    0,
			PerPage: 100,
		},
	}

	for {
		repositories, resp, err := client.Repositories.ListByUser(ctx, githubUser, opts)
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

func runSingleRepo(repoName, ref string) error {
	if githubToken == "" {
		return fmt.Errorf("GITHUB_TOKEN environment variable is required")
	}

	// Move out of the go directory
	if err := os.Chdir(path.Dir(must(os.Getwd()))); err != nil {
		return fmt.Errorf("failed to change directory: %w", err)
	}

	// Ensure required directories exist
	for _, dir := range requiredDirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	ctx := context.Background()
	client := github.NewClient(nil).WithAuthToken(githubToken)

	// Get repository info
	repository, _, err := client.Repositories.Get(ctx, githubUser, repoName)
	if err != nil {
		return fmt.Errorf("failed to get repository %s: %w", repoName, err)
	}

	// Get deploy config directory
	_, dirContent, _, err := client.Repositories.GetContents(ctx, githubUser, repoName, deployDir, &github.RepositoryContentGetOptions{
		Ref: ref,
	})
	if err != nil {
		return fmt.Errorf("failed to get deploy config for %s at ref %s: %w", repoName, ref, err)
	}

	if len(dirContent) == 0 {
		return fmt.Errorf("no deploy config found in %s at ref %s", repoName, ref)
	}

	displayURL := repository.GetHTMLURL()
	displayURL = strings.TrimPrefix(displayURL, "https://")
	displayURL = strings.ReplaceAll(displayURL, "/", "_")

	// Create directory
	dir := path.Join("docker", displayURL)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Get commit SHA for the specified ref
	commit, _, err := client.Repositories.GetCommit(ctx, githubUser, repoName, ref, nil)
	if err != nil {
		return fmt.Errorf("failed to get commit for %s at ref %s: %w", repoName, ref, err)
	}
	commitSHA := commit.GetSHA()

	repoURL := repository.GetHTMLURL()

	for _, content := range dirContent {
		rawURL := content.GetDownloadURL()
		if rawURL == "" {
			continue
		}

		filename := path.Join(dir, content.GetName())
		if err := downloadFile(rawURL, filename); err != nil {
			return fmt.Errorf("failed to download %s: %w", content.GetName(), err)
		}

		// Replace SHA placeholders
		if err := replaceShaPlaceholders(filename, commitSHA); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to replace SHA placeholders in %s: %v\n", content.GetName(), err)
		}

		// Add header comments
		if err := addHeaderComments(filename, repoURL, ref); err != nil {
			return fmt.Errorf("failed to add header comments to %s: %w", content.GetName(), err)
		}
	}

	fmt.Printf("Successfully scanned %s at ref %s\n", repoName, ref)
	return nil
}

func addHeaderComments(filename, repoURL, ref string) error {
	content, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	header := fmt.Sprintf("# Repo: %s\n# Ref: %s\n\n", repoURL, ref)
	updatedContent := header + string(content)

	if err := os.WriteFile(filename, []byte(updatedContent), 0644); err != nil {
		return fmt.Errorf("failed to write updated file: %w", err)
	}

	return nil
}
