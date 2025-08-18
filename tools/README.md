# Infrastructure Tools

A collection of Go-based tools for managing containerized deployments and repository tracking.

## Overview

This repository contains three main tools that work together to provide automated deployment capabilities:

- **Coach** - A gRPC service for building and deploying containerized applications
- **Coach Assistant** - A CLI client for interacting with the Coach service  
- **Scout** - A tool for tracking repositories and synchronizing deployment configurations

## Tools

### Coach (`cmd/coach`)

A gRPC server that handles container image building and service deployment operations.

**Features:**
- Build Docker images from Git repositories
- Deploy services using Docker Compose
- Secure authentication via Bearer tokens
- Automated cleanup of temporary files

**Environment Variables:**
- `COACH_AUTH_TOKEN` - Required authentication token for gRPC requests

**gRPC Service Methods:**
- `Assemble` - Clone a repository, build a Docker image, and push to registry
- `Start` - Download service configuration and start services via Docker Compose

### Coach Assistant (`cmd/coachassistant`)

A CLI client for communicating with the Coach gRPC service.

**Commands:**

#### `assemble`
Build and push a container image from a Git repository.

```bash
coachassistant assemble \
  --repo <repository-name> \
  --ref <git-reference> \
  --image <image-name> \
  [--dockerfile <dockerfile-path>] \
  [--context <build-context>] \
  [--tag <latest|sha|unspecified>]
```

#### `start`
Deploy a service using its deployment configuration.

```bash
coachassistant start \
  --service <service-name> \
  --ref <git-reference>
```

**Environment Variables:**
- `COACH_AUTH_TOKEN` - Required authentication token

**Options:**
- `--server` - Coach server address (default: coach.baileys.dev:443)
- `--insecure` - Use insecure connection (default: false)

### Scout (`cmd/scout`)

A repository tracking tool that discovers and downloads deployment configurations from GitHub repositories.

**Commands:**

#### `scan`
Scan all repositories for the configured user and download their deployment configurations.

```bash
scout scan
```

#### `repo`
Scan a specific repository at a given reference.

```bash
scout repo <repository-name> [git-reference]
```

**Features:**
- Automatically discovers repositories with deployment configurations
- Downloads files from the `config` directory of each repository
- Replaces `{{sha}}` placeholders with actual commit SHAs
- Organizes configurations in `docker/` directory structure
- Adds header comments with repository URL and reference information

**Environment Variables:**
- `GITHUB_TOKEN` - Required GitHub personal access token

**Output Structure:**
```
docker/
├── github.com_baely_repo1/
│   ├── deploy.yaml
│   └── config.json
└── github.com_baely_repo2/
    └── deploy.yaml
```

## Protocol Buffers

The tools use gRPC communication defined in `squad/v1alpha1/coach.proto`:

### AssembleRequest
- `repo` - Repository name
- `ref` - Git reference (branch, tag, or SHA)  
- `dockerfile_location` - Optional Dockerfile path
- `context_location` - Optional build context path
- `image` - Target image name
- `tag` - Tag strategy (TAG_LATEST, TAG_SHA, TAG_UNSPECIFIED)

### StartRequest
- `service` - Service name to deploy
- `ref` - Git reference for configuration

## Building

```bash
go build ./cmd/coach
go build ./cmd/coachassistant  
go build ./cmd/scout
```

## Dependencies

- Go 1.24+
- Docker (for Coach image building)
- Docker Compose (for service deployment)
- Git (for repository operations)
- Protocol Buffers compiler (for development)

## Usage Example

1. **Build and push an image:**
   ```bash
   export COACH_AUTH_TOKEN="your-token"
   coachassistant assemble --repo myapp --ref main --image myapp --tag latest
   ```

2. **Deploy a service:**
   ```bash
   coachassistant start --service github.com_baely_myapp --ref main
   ```

3. **Sync deployment configurations:**
   ```bash
   export GITHUB_TOKEN="your-github-token"
   scout scan
   ```