// Scout tracks all repositories for a user and pulls down deploy config into the appropriate location.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
)

func main() {
	// Move out of the go directory
	_ = os.Chdir(path.Dir(must(os.Getwd())))

	ctx := context.Background()

	err := fetchDeployConfigs(ctx)
	if err != nil {
		fmt.Println("failed to fetch deploy configs:", err)
		panic(err)
	}

}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// fetchDeployConfigs finds all GitHub repositories that contain deploy config.
func fetchDeployConfigs(ctx context.Context) error {
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
			continue
		}

		if len(dirContent) == 0 {
			continue
		}

		displayURL := repository.GetHTMLURL()
		displayURL = strings.TrimPrefix(displayURL, "https://")
		displayURL = strings.ReplaceAll(displayURL, "/", "_")

		// Create directory if required.
		dir := path.Join("docker", displayURL)
		_, err = os.Stat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			err = os.Mkdir(dir, os.ModePerm)
			if err != nil {
				continue
			}
		} else if err != nil {
			continue
		}

		for _, content := range dirContent {
			rawURL := content.GetDownloadURL()

			resp, err := http.Get(rawURL)
			if err != nil || resp.StatusCode != http.StatusOK {
				continue
			}
			defer func() {
				_ = resp.Body.Close()
			}()

			b, err := io.ReadAll(resp.Body)
			if err != nil {
				continue
			}

			filename := path.Join(dir, content.GetName())
			if err := os.WriteFile(filename, b, os.ModePerm); err != nil {
				continue
			}
		}
	}

	return nil
}
