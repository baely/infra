// Package coach is responsible for getting services deployed.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/go-github/v74/github"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	squadv1alpha1 "github.com/baely/infra/tools/gen/squad/v1alpha1"
)

func main() {
	authToken := os.Getenv("COACH_AUTH_TOKEN")
	if authToken == "" {
		log.Fatal("COACH_AUTH_TOKEN environment variable is required")
	}

	service := &coachService{}

	server := grpc.NewServer(
		grpc.UnaryInterceptor(authInterceptor(authToken)),
	)
	squadv1alpha1.RegisterCoachServiceServer(server, service)

	lis, err := net.Listen("tcp", "0.0.0.0:8080")
	if err != nil {
		log.Fatalf("failed to start tcp listener: %v", err)
	}

	fmt.Println("listening on :8080")

	if err = server.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

type coachService struct {
	squadv1alpha1.UnimplementedCoachServiceServer
}

func (s *coachService) Assemble(ctx context.Context, req *squadv1alpha1.AssembleRequest) (*squadv1alpha1.AssembleResponse, error) {
	if err := validateAssembleRequest(req); err != nil {
		return nil, err
	}

	repo := fmt.Sprintf("https://github.com/baely/%s", req.Repo)

	tempDir, err := os.MkdirTemp("", fmt.Sprintf("coach-assemble-%s-", req.Repo))
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			log.Printf("Warning: failed to cleanup temp directory %s: %v", tempDir, err)
		}
	}()

	repoDir := filepath.Join(tempDir, req.Repo)

	if err := s.cloneRepo(repo, repoDir); err != nil {
		return nil, fmt.Errorf("failed to clone repository: %w", err)
	}

	if err := s.checkoutRef(repoDir, req.Ref); err != nil {
		return nil, fmt.Errorf("failed to checkout ref %s: %w", req.Ref, err)
	}

	dockerTag, err := getDockerTag(req)
	if err != nil {
		return nil, err
	}

	dockerImage := fmt.Sprintf("registry.baileys.dev/%s:%s", req.Image, dockerTag)

	dockerfile := getStringOrDefault(req.DockerfileLocation, "Dockerfile")
	dockerContext := getStringOrDefault(req.ContextLocation, ".")

	if err := s.buildDockerImage(repoDir, dockerImage, dockerfile, dockerContext); err != nil {
		return nil, fmt.Errorf("failed to build docker image: %w", err)
	}

	return &squadv1alpha1.AssembleResponse{}, nil
}

func (s *coachService) Start(ctx context.Context, req *squadv1alpha1.StartRequest) (*squadv1alpha1.StartResponse, error) {
	log.Printf("Starting deployment for service: %s, ref: %s", req.Service, req.Ref)
	
	if err := validateStartRequest(req); err != nil {
		log.Printf("Validation failed for start request: %v", err)
		return nil, err
	}
	log.Printf("Start request validation passed")

	log.Printf("Downloading service config for %s", req.Service)
	workDir, err := s.downloadServiceConfig(ctx, req.Service, req.Ref)
	if err != nil {
		log.Printf("Failed to download service config: %v", err)
		return nil, fmt.Errorf("failed to download service config: %w", err)
	}
	log.Printf("Service config downloaded to: %s", workDir)
	
	defer func() {
		log.Printf("Cleaning up temp directory: %s", workDir)
		if err := os.RemoveAll(workDir); err != nil {
			log.Printf("Warning: failed to cleanup temp directory %s: %v", workDir, err)
		} else {
			log.Printf("Successfully cleaned up temp directory")
		}
	}()

	log.Printf("Validating deploy file in: %s", workDir)
	if err := validateDeployFile(workDir); err != nil {
		log.Printf("Deploy file validation failed: %v", err)
		return nil, err
	}
	log.Printf("Deploy file validation passed")

	log.Printf("Pulling docker images for service: %s", req.Service)
	if err := s.runDockerCompose(workDir, "pull"); err != nil {
		log.Printf("Failed to pull docker images: %v", err)
		return nil, fmt.Errorf("failed to pull images: %w", err)
	}
	log.Printf("Successfully pulled docker images")

	log.Printf("Starting service containers for: %s", req.Service)
	if err := s.runDockerCompose(workDir, "up", "-d"); err != nil {
		log.Printf("Failed to start service containers: %v", err)
		return nil, fmt.Errorf("failed to start service: %w", err)
	}
	log.Printf("Successfully started service: %s", req.Service)

	return &squadv1alpha1.StartResponse{}, nil
}

func (s *coachService) runDockerCompose(workDir string, args ...string) error {
	fullArgs := append([]string{"compose", "-f", "deploy.yaml"}, args...)
	log.Printf("Running docker command: docker %v", fullArgs)
	log.Printf("Working directory: %s", workDir)
	
	cmd := exec.Command("docker", fullArgs...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		log.Printf("Docker compose command failed: %v", err)
		return err
	}
	
	log.Printf("Docker compose command completed successfully")
	return nil
}

func (s *coachService) downloadServiceConfig(ctx context.Context, serviceName, ref string) (string, error) {
	log.Printf("Downloading service config for %s at ref %s", serviceName, ref)
	client := github.NewClient(nil)

	servicePath := path.Join("docker", serviceName)
	log.Printf("Fetching contents from GitHub path: %s", servicePath)
	_, dirContent, _, err := client.Repositories.GetContents(ctx, "baely", "infra", servicePath, &github.RepositoryContentGetOptions{
		Ref: ref,
	})
	if err != nil {
		log.Printf("Failed to get GitHub contents for %s: %v", servicePath, err)
		return "", fmt.Errorf("failed to get service directory contents: %w", err)
	}

	if len(dirContent) == 0 {
		log.Printf("No files found in GitHub service directory: %s", servicePath)
		return "", fmt.Errorf("no files found in service directory %s", servicePath)
	}
	log.Printf("Found %d files in GitHub service directory", len(dirContent))

	tempDir, err := os.MkdirTemp("", fmt.Sprintf("coach-service-%s-", serviceName))
	if err != nil {
		log.Printf("Failed to create temp directory: %v", err)
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	log.Printf("Created temp directory: %s", tempDir)

	serviceDir := filepath.Join(tempDir, serviceName)
	if err := os.MkdirAll(serviceDir, 0755); err != nil {
		log.Printf("Failed to create service subdirectory %s: %v", serviceDir, err)
		return "", fmt.Errorf("failed to create service subdirectory: %w", err)
	}
	log.Printf("Created service directory: %s", serviceDir)

	downloadedCount := 0
	for _, content := range dirContent {
		rawURL := content.GetDownloadURL()
		if rawURL == "" {
			log.Printf("Skipping file %s (no download URL)", content.GetName())
			continue
		}

		filename := filepath.Join(serviceDir, content.GetName())
		log.Printf("Downloading file: %s -> %s", content.GetName(), filename)
		if err := downloadFileToPath(rawURL, filename); err != nil {
			log.Printf("Warning: failed to download file %s: %v", content.GetName(), err)
			continue
		}
		downloadedCount++
	}
	log.Printf("Successfully downloaded %d files from GitHub", downloadedCount)

	// Copy files from mounted services directory if it exists
	mountedServicePath := fmt.Sprintf("/app/services/%s", serviceName)
	log.Printf("Checking for mounted service files at: %s", mountedServicePath)
	if err := copyMountedServiceFiles(mountedServicePath, serviceDir); err != nil {
		log.Printf("Warning: failed to copy mounted service files from %s: %v", mountedServicePath, err)
	} else {
		log.Printf("Successfully copied mounted service files from: %s", mountedServicePath)
	}

	log.Printf("Service config setup complete for %s at: %s", serviceName, serviceDir)
	return serviceDir, nil
}

func (s *coachService) cloneRepo(repoURL, destDir string) error {
	cmd := exec.Command("git", "clone", repoURL, destDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (s *coachService) checkoutRef(repoDir, ref string) error {
	cmd := exec.Command("git", "checkout", ref)
	cmd.Dir = repoDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (s *coachService) buildDockerImage(repoDir, imageName, dockerfile, context string) error {
	cmd := exec.Command("docker", "build",
		"--tag", imageName,
		"--push",
		"--platform", "linux/amd64",
		"--file", dockerfile,
		context)
	cmd.Dir = repoDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func validateAssembleRequest(req *squadv1alpha1.AssembleRequest) error {
	if req.Repo == "" {
		return fmt.Errorf("repo name is required")
	}
	if req.Ref == "" {
		return fmt.Errorf("ref is required")
	}
	if req.Image == "" {
		return fmt.Errorf("image name is required")
	}
	return nil
}

func validateStartRequest(req *squadv1alpha1.StartRequest) error {
	log.Printf("Validating start request - Service: %s, Ref: %s", req.Service, req.Ref)
	
	if req.Service == "" {
		log.Printf("Validation failed: service name is required")
		return fmt.Errorf("service name is required")
	}
	if req.Ref == "" {
		log.Printf("Validation failed: ref is required")
		return fmt.Errorf("ref is required")
	}
	if req.Service == "github.com_baely_infra" {
		log.Printf("Validation failed: coach cannot deploy itself")
		return fmt.Errorf("coach cannot deploy coach")
	}
	
	log.Printf("Start request validation successful")
	return nil
}

func getDockerTag(req *squadv1alpha1.AssembleRequest) (string, error) {
	switch req.Tag {
	case squadv1alpha1.AssembleRequest_TAG_LATEST:
		return "latest", nil
	case squadv1alpha1.AssembleRequest_TAG_SHA:
		return req.Ref, nil
	default:
		return "", fmt.Errorf("invalid docker tag")
	}
}

func getStringOrDefault(ptr *string, defaultValue string) string {
	if ptr != nil {
		return *ptr
	}
	return defaultValue
}

func validateDeployFile(workDir string) error {
	deployYamlPath := filepath.Join(workDir, "deploy.yaml")
	log.Printf("Validating deploy file exists at: %s", deployYamlPath)
	
	if _, err := os.Stat(deployYamlPath); os.IsNotExist(err) {
		log.Printf("Deploy file validation failed: deploy.yaml not found at %s", deployYamlPath)
		return fmt.Errorf("deploy.yaml not found in service directory")
	}
	
	log.Printf("Deploy file validation successful: found deploy.yaml")
	return nil
}

func downloadFileToPath(url, filepath string) error {
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

	return os.WriteFile(filepath, b, 0644)
}

func copyMountedServiceFiles(sourcePath, destPath string) error {
	log.Printf("Attempting to copy mounted files from %s to %s", sourcePath, destPath)
	
	if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
		log.Printf("Mounted service directory does not exist: %s", sourcePath)
		return nil
	}
	log.Printf("Found mounted service directory: %s", sourcePath)

	copiedFiles := 0
	copiedDirs := 0
	
	err := filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("Error walking path %s: %v", path, err)
			return err
		}

		relPath, err := filepath.Rel(sourcePath, path)
		if err != nil {
			log.Printf("Error getting relative path for %s: %v", path, err)
			return err
		}

		destFile := filepath.Join(destPath, relPath)

		if info.IsDir() {
			log.Printf("Creating directory: %s", destFile)
			if err := os.MkdirAll(destFile, info.Mode()); err != nil {
				log.Printf("Failed to create directory %s: %v", destFile, err)
				return err
			}
			copiedDirs++
			return nil
		}

		log.Printf("Copying file: %s -> %s (size: %d bytes)", path, destFile, info.Size())
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("Failed to read file %s: %v", path, err)
			return err
		}

		if err := os.WriteFile(destFile, data, info.Mode()); err != nil {
			log.Printf("Failed to write file %s: %v", destFile, err)
			return err
		}
		copiedFiles++
		return nil
	})
	
	if err != nil {
		log.Printf("Error during mounted file copy operation: %v", err)
		return err
	}
	
	log.Printf("Successfully copied %d files and %d directories from mounted service directory", copiedFiles, copiedDirs)
	return nil
}

func authInterceptor(expectedToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "metadata not found")
		}

		authHeader := md.Get("authorization")
		if len(authHeader) == 0 {
			return nil, status.Error(codes.Unauthenticated, "authorization header required")
		}

		token := strings.TrimPrefix(authHeader[0], "Bearer ")
		if token != expectedToken {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		return handler(ctx, req)
	}
}
