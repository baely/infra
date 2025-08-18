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
	if err := validateStartRequest(req); err != nil {
		return nil, err
	}

	workDir, err := s.downloadServiceConfig(ctx, req.Service, req.Ref)
	if err != nil {
		return nil, fmt.Errorf("failed to download service config: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(workDir); err != nil {
			log.Printf("Warning: failed to cleanup temp directory %s: %v", workDir, err)
		}
	}()

	if err := validateDeployFile(workDir); err != nil {
		return nil, err
	}

	if err := s.runDockerCompose(workDir, "pull"); err != nil {
		return nil, fmt.Errorf("failed to pull images: %w", err)
	}

	if err := s.runDockerCompose(workDir, "up", "-d"); err != nil {
		return nil, fmt.Errorf("failed to start service: %w", err)
	}

	return &squadv1alpha1.StartResponse{}, nil
}

func (s *coachService) runDockerCompose(workDir string, args ...string) error {
	cmd := exec.Command("docker", append([]string{"compose", "-f", "deploy.yaml"}, args...)...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (s *coachService) downloadServiceConfig(ctx context.Context, serviceName, ref string) (string, error) {
	client := github.NewClient(nil)

	servicePath := path.Join("docker", serviceName)
	_, dirContent, _, err := client.Repositories.GetContents(ctx, "baely", "infra", servicePath, &github.RepositoryContentGetOptions{
		Ref: ref,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get service directory contents: %w", err)
	}

	if len(dirContent) == 0 {
		return "", fmt.Errorf("no files found in service directory %s", servicePath)
	}

	tempDir, err := os.MkdirTemp("", fmt.Sprintf("coach-service-%s-", serviceName))
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	serviceDir := filepath.Join(tempDir, serviceName)
	if err := os.MkdirAll(serviceDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create service subdirectory: %w", err)
	}

	for _, content := range dirContent {
		rawURL := content.GetDownloadURL()
		if rawURL == "" {
			continue
		}

		filename := filepath.Join(serviceDir, content.GetName())
		if err := downloadFileToPath(rawURL, filename); err != nil {
			log.Printf("Warning: failed to download file %s: %v", content.GetName(), err)
			continue
		}
	}

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
	if req.Service == "" {
		return fmt.Errorf("service name is required")
	}
	if req.Ref == "" {
		return fmt.Errorf("ref is required")
	}
	if req.Service == "github.com_baely_infra" {
		return fmt.Errorf("coach cannot deploy coach")
	}
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
	if _, err := os.Stat(deployYamlPath); os.IsNotExist(err) {
		return fmt.Errorf("deploy.yaml not found in service directory")
	}
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
