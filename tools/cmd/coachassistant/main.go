package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/baely/infra/tools/gen/squad/v1alpha1"
)

var (
	authToken string
	serverAddr string
	insecureConn bool

	repo string
	ref string
	dockerfileLocation string
	contextLocation string
	image string
	tag string

	service string
	startRef string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "coachassistant",
		Short: "Coach service gRPC client",
		Long:  "A CLI tool for interacting with the Coach service via gRPC",
	}

	authToken = os.Getenv("COACH_AUTH_TOKEN")
	rootCmd.PersistentFlags().StringVar(&serverAddr, "server", "coach.baileys.dev:443", "Server address")
	rootCmd.PersistentFlags().BoolVar(&insecureConn, "insecure", false, "Use insecure connection")

	assembleCmd := &cobra.Command{
		Use:   "assemble",
		Short: "Assemble a container image",
		RunE:  runAssemble,
	}

	assembleCmd.Flags().StringVar(&repo, "repo", "", "Repository name (required)")
	assembleCmd.Flags().StringVar(&ref, "ref", "", "Git reference (required)")
	assembleCmd.Flags().StringVar(&dockerfileLocation, "dockerfile", "", "Dockerfile location")
	assembleCmd.Flags().StringVar(&contextLocation, "context", "", "Build context location")
	assembleCmd.Flags().StringVar(&image, "image", "", "Image name (required)")
	assembleCmd.Flags().StringVar(&tag, "tag", "sha", "Tag type: unspecified, latest, sha")
	assembleCmd.MarkFlagRequired("repo")
	assembleCmd.MarkFlagRequired("ref")
	assembleCmd.MarkFlagRequired("image")

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start a service",
		RunE:  runStart,
	}

	startCmd.Flags().StringVar(&service, "service", "", "Service name (required)")
	startCmd.Flags().StringVar(&startRef, "ref", "", "Git reference (required)")
	startCmd.MarkFlagRequired("service")
	startCmd.MarkFlagRequired("ref")

	rootCmd.AddCommand(assembleCmd, startCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func createClient() (squadv1alpha1.CoachServiceClient, error) {
	if authToken == "" {
		return nil, fmt.Errorf("COACH_AUTH_TOKEN environment variable is required")
	}

	var opts []grpc.DialOption
	if insecureConn {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		creds := credentials.NewClientTLSFromCert(nil, "")
		opts = append(opts, grpc.WithTransportCredentials(creds))
	}

	conn, err := grpc.NewClient(serverAddr, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to server: %w", err)
	}

	return squadv1alpha1.NewCoachServiceClient(conn), nil
}

func runAssemble(cmd *cobra.Command, args []string) error {
	client, err := createClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", fmt.Sprintf("Bearer %s", authToken))

	tagEnum := squadv1alpha1.AssembleRequest_TAG_UNSPECIFIED
	switch tag {
	case "latest":
		tagEnum = squadv1alpha1.AssembleRequest_TAG_LATEST
	case "sha":
		tagEnum = squadv1alpha1.AssembleRequest_TAG_SHA
	case "unspecified":
		tagEnum = squadv1alpha1.AssembleRequest_TAG_UNSPECIFIED
	default:
		return fmt.Errorf("invalid tag type: %s (must be: unspecified, latest, sha)", tag)
	}

	req := &squadv1alpha1.AssembleRequest{
		Repo:  repo,
		Ref:   ref,
		Image: image,
		Tag:   tagEnum,
	}

	if dockerfileLocation != "" {
		req.DockerfileLocation = &dockerfileLocation
	}
	if contextLocation != "" {
		req.ContextLocation = &contextLocation
	}

	_, err = client.Assemble(ctx, req)
	if err != nil {
		return fmt.Errorf("assemble failed: %w", err)
	}

	fmt.Println("Assemble request completed successfully")
	return nil
}

func runStart(cmd *cobra.Command, args []string) error {
	client, err := createClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", fmt.Sprintf("Bearer %s", authToken))

	req := &squadv1alpha1.StartRequest{
		Service: service,
		Ref:     startRef,
	}

	_, err = client.Start(ctx, req)
	if err != nil {
		return fmt.Errorf("start failed: %w", err)
	}

	fmt.Println("Start request completed successfully")
	return nil
}
