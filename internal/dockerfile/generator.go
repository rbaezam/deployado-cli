package dockerfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/deployado/cli/internal/detect"
)

// Generator generates Dockerfiles for detected stacks
type Generator struct {
	dir string
}

// NewGenerator creates a new Dockerfile generator
func NewGenerator(dir string) *Generator {
	return &Generator{dir: dir}
}

// Generate creates a Dockerfile based on the detected stack
func (g *Generator) Generate(info *detect.StackInfo) (string, error) {
	switch info.Stack {
	case detect.StackPhoenix:
		return g.generatePhoenix(info)
	case detect.StackElixir:
		return g.generateElixir(info)
	case detect.StackDjango:
		return g.generateDjango(info)
	case detect.StackFastAPI:
		return g.generateFastAPI(info)
	case detect.StackFlask:
		return g.generateFlask(info)
	case detect.StackPython:
		return g.generatePython(info)
	case detect.StackRails:
		return g.generateRails(info)
	case detect.StackRuby:
		return g.generateRuby(info)
	case detect.StackDotNet:
		return g.generateDotNet(info)
	case detect.StackNode:
		return g.generateNode(info)
	case detect.StackStatic:
		return g.generateStatic(info)
	default:
		return "", fmt.Errorf("unsupported stack: %s", info.Stack)
	}
}

// WriteDockerfile writes the generated Dockerfile to disk
func (g *Generator) WriteDockerfile(content string) error {
	path := filepath.Join(g.dir, "Dockerfile")
	return os.WriteFile(path, []byte(content), 0644)
}

// ExistsDockerfile checks if a Dockerfile already exists
func (g *Generator) ExistsDockerfile() bool {
	_, err := os.Stat(filepath.Join(g.dir, "Dockerfile"))
	return err == nil
}

// Template helpers

func executeTemplate(tmpl string, data interface{}) (string, error) {
	t, err := template.New("dockerfile").Parse(tmpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// Phoenix Dockerfile
func (g *Generator) generatePhoenix(info *detect.StackInfo) (string, error) {
	// Check if assets/package.json exists (npm dependencies needed)
	hasNpmDeps := g.fileExists("assets/package.json")

	data := struct {
		*detect.StackInfo
		HasNpmDeps bool
	}{
		StackInfo:  info,
		HasNpmDeps: hasNpmDeps,
	}

	tmpl := `# Build stage
FROM elixir:1.16-alpine AS builder

# Install build dependencies
RUN apk add --no-cache build-base git nodejs npm

WORKDIR /app

# Install hex and rebar
RUN mix local.hex --force && mix local.rebar --force

# Set build environment
ENV MIX_ENV=prod

# Install dependencies
COPY mix.exs mix.lock ./
RUN mix deps.get --only prod

# Copy config files
COPY config config

# Compile dependencies
RUN mix deps.compile

# Copy assets and source code
COPY assets assets
COPY priv priv
COPY lib lib
{{if .HasNpmDeps}}
# Install npm dependencies for JavaScript assets
RUN cd assets && npm install
{{end}}
# Compile the project (generates colocated hooks for LiveView 1.1+)
RUN mix compile

# Compile assets
RUN mix assets.deploy

# Build release
RUN mix release

# Runtime stage
FROM alpine:3.19 AS app

RUN apk add --no-cache libstdc++ openssl ncurses-libs

WORKDIR /app

# Copy release from builder
COPY --from=builder /app/_build/prod/rel/{{.AppName}} ./

ENV PHX_HOST=0.0.0.0
ENV PORT={{.Port}}

EXPOSE {{.Port}}

CMD ["bin/{{.AppName}}", "start"]
`
	return executeTemplate(tmpl, data)
}

// fileExists checks if a file exists relative to the generator's directory
func (g *Generator) fileExists(relativePath string) bool {
	_, err := os.Stat(filepath.Join(g.dir, relativePath))
	return err == nil
}

// Elixir Dockerfile (non-Phoenix)
func (g *Generator) generateElixir(info *detect.StackInfo) (string, error) {
	tmpl := `# Build stage
FROM elixir:1.16-alpine AS builder

RUN apk add --no-cache build-base git

WORKDIR /app

RUN mix local.hex --force && mix local.rebar --force

ENV MIX_ENV=prod

COPY mix.exs mix.lock ./
RUN mix deps.get --only prod

COPY config config
COPY lib lib

RUN mix compile
RUN mix release

# Runtime stage
FROM alpine:3.19 AS app

RUN apk add --no-cache libstdc++ openssl ncurses-libs

WORKDIR /app

COPY --from=builder /app/_build/prod/rel/{{.AppName}} ./

ENV PORT={{.Port}}

EXPOSE {{.Port}}

CMD ["bin/{{.AppName}}", "start"]
`
	return executeTemplate(tmpl, info)
}

// Django Dockerfile
func (g *Generator) generateDjango(info *detect.StackInfo) (string, error) {
	tmpl := `FROM python:3.12-slim

RUN apt-get update && apt-get install -y \
    libpq-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
RUN pip install --no-cache-dir gunicorn

COPY . .

# Collect static files
RUN python manage.py collectstatic --noinput || true

ENV PORT={{.Port}}

EXPOSE {{.Port}}

CMD ["sh", "-c", "gunicorn --bind 0.0.0.0:$PORT --workers 2 config.wsgi:application"]
`
	return executeTemplate(tmpl, info)
}

// FastAPI Dockerfile
func (g *Generator) generateFastAPI(info *detect.StackInfo) (string, error) {
	tmpl := `FROM python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

ENV PORT={{.Port}}

EXPOSE {{.Port}}

CMD ["sh", "-c", "uvicorn main:app --host 0.0.0.0 --port $PORT"]
`
	return executeTemplate(tmpl, info)
}

// Flask Dockerfile
func (g *Generator) generateFlask(info *detect.StackInfo) (string, error) {
	tmpl := `FROM python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
RUN pip install --no-cache-dir gunicorn

COPY . .

ENV PORT={{.Port}}

EXPOSE {{.Port}}

CMD ["sh", "-c", "gunicorn --bind 0.0.0.0:$PORT --workers 2 app:app"]
`
	return executeTemplate(tmpl, info)
}

// Generic Python Dockerfile
func (g *Generator) generatePython(info *detect.StackInfo) (string, error) {
	tmpl := `FROM python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

ENV PORT={{.Port}}

EXPOSE {{.Port}}

CMD ["python", "main.py"]
`
	return executeTemplate(tmpl, info)
}

// Rails Dockerfile
func (g *Generator) generateRails(info *detect.StackInfo) (string, error) {
	tmpl := `# Build stage
FROM ruby:3.3-slim AS builder

RUN apt-get update && apt-get install -y \
    build-essential \
    libpq-dev \
    nodejs \
    npm \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY Gemfile Gemfile.lock ./
RUN bundle config set --local deployment 'true'
RUN bundle config set --local without 'development test'
RUN bundle install

COPY . .

# Precompile assets
RUN SECRET_KEY_BASE=dummy bundle exec rails assets:precompile

# Runtime stage
FROM ruby:3.3-slim

RUN apt-get update && apt-get install -y \
    libpq-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /app /app
COPY --from=builder /usr/local/bundle /usr/local/bundle

ENV RAILS_ENV=production
ENV RAILS_LOG_TO_STDOUT=true
ENV RAILS_SERVE_STATIC_FILES=true
ENV PORT={{.Port}}

EXPOSE {{.Port}}

CMD ["sh", "-c", "bundle exec rails server -b 0.0.0.0 -p $PORT"]
`
	return executeTemplate(tmpl, info)
}

// Generic Ruby Dockerfile
func (g *Generator) generateRuby(info *detect.StackInfo) (string, error) {
	tmpl := `FROM ruby:3.3-slim

RUN apt-get update && apt-get install -y \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY Gemfile Gemfile.lock ./
RUN bundle install

COPY . .

ENV PORT={{.Port}}

EXPOSE {{.Port}}

CMD ["ruby", "app.rb"]
`
	return executeTemplate(tmpl, info)
}

// .NET Dockerfile
func (g *Generator) generateDotNet(info *detect.StackInfo) (string, error) {
	projectInfo := detect.DetectDotNetProject(g.dir)
	if projectInfo == nil {
		return "", fmt.Errorf("could not detect .NET project")
	}

	// Determine if we need to use a preview image for .NET 9.0
	// .NET 9.0 was released in November 2024, so it should be stable
	sdkVersion := projectInfo.SDKVersion

	data := struct {
		*detect.StackInfo
		Framework   string
		SDKVersion  string
		EntryPoint  string
		ProjectDir  string
		HasSubdir   bool
		ProjectName string
	}{
		StackInfo:   info,
		Framework:   projectInfo.TargetFramework,
		SDKVersion:  sdkVersion,
		EntryPoint:  projectInfo.AppName + ".dll",
		ProjectDir:  projectInfo.CsprojDir,
		HasSubdir:   projectInfo.CsprojDir != "",
		ProjectName: projectInfo.AppName,
	}

	// Use different template based on project structure
	var tmpl string
	if projectInfo.CsprojDir != "" {
		// Project is in a subdirectory
		tmpl = `# Build stage
FROM mcr.microsoft.com/dotnet/sdk:{{.SDKVersion}} AS build

WORKDIR /src

# Copy solution and project files first for layer caching
{{if .HasSubdir}}COPY {{.ProjectDir}}/*.csproj ./{{.ProjectDir}}/
WORKDIR /src/{{.ProjectDir}}
{{else}}COPY *.csproj ./
{{end}}
RUN dotnet restore

# Copy everything and build
WORKDIR /src
COPY . .

{{if .HasSubdir}}WORKDIR /src/{{.ProjectDir}}
{{end}}RUN dotnet publish -c Release -o /app --no-restore

# Runtime stage
FROM mcr.microsoft.com/dotnet/aspnet:{{.SDKVersion}}

WORKDIR /app

COPY --from=build /app .

# Configure ASP.NET Core
ENV ASPNETCORE_URLS=http://+:{{.Port}}
ENV ASPNETCORE_ENVIRONMENT=Production
ENV PORT={{.Port}}

EXPOSE {{.Port}}

ENTRYPOINT ["dotnet", "{{.EntryPoint}}"]
`
	} else {
		// Project is in root directory
		tmpl = `# Build stage
FROM mcr.microsoft.com/dotnet/sdk:{{.SDKVersion}} AS build

WORKDIR /src

# Copy project file for layer caching
COPY *.csproj ./
RUN dotnet restore

# Copy everything and build
COPY . .
RUN dotnet publish -c Release -o /app --no-restore

# Runtime stage
FROM mcr.microsoft.com/dotnet/aspnet:{{.SDKVersion}}

WORKDIR /app

COPY --from=build /app .

# Configure ASP.NET Core
ENV ASPNETCORE_URLS=http://+:{{.Port}}
ENV ASPNETCORE_ENVIRONMENT=Production
ENV PORT={{.Port}}

EXPOSE {{.Port}}

ENTRYPOINT ["dotnet", "{{.EntryPoint}}"]
`
	}

	return executeTemplate(tmpl, data)
}

// Node.js Dockerfile
func (g *Generator) generateNode(info *detect.StackInfo) (string, error) {
	pm := detect.GetPackageManager(g.dir)
	startCmd := detect.GetNodeStartCommand(g.dir)

	data := struct {
		*detect.StackInfo
		PackageManager string
		StartCommand   string
	}{
		StackInfo:      info,
		PackageManager: pm,
		StartCommand:   startCmd,
	}

	tmpl := `FROM node:20-alpine

WORKDIR /app

COPY package*.json ./
{{if eq .PackageManager "pnpm"}}
RUN npm install -g pnpm && pnpm install --frozen-lockfile
{{else if eq .PackageManager "yarn"}}
COPY yarn.lock ./
RUN yarn install --frozen-lockfile
{{else}}
RUN npm ci --only=production
{{end}}

COPY . .

ENV NODE_ENV=production
ENV PORT={{.Port}}

EXPOSE {{.Port}}

CMD ["{{.StartCommand}}"]
`
	return executeTemplate(tmpl, data)
}

// Static site Dockerfile
func (g *Generator) generateStatic(info *detect.StackInfo) (string, error) {
	siteInfo := detect.DetectStaticSite(g.dir)

	pm := detect.GetPackageManager(g.dir)
	buildDir := detect.GetStaticBuildDir(g.dir)
	frameworkName := "Static Site"
	envPrefix := ""

	if siteInfo != nil {
		pm = siteInfo.PackageManager
		buildDir = siteInfo.BuildDir
		frameworkName = siteInfo.FrameworkName
		envPrefix = siteInfo.EnvPrefix
	}

	// Extract env vars to pass as build args
	envVars := detect.ExtractStaticEnvVars(g.dir)
	var buildArgs []string
	for _, ev := range envVars {
		if ev.Key != "" {
			buildArgs = append(buildArgs, ev.Key)
		}
	}

	data := struct {
		*detect.StackInfo
		PackageManager string
		BuildDir       string
		FrameworkName  string
		EnvPrefix      string
		BuildArgs      []string
		HasBuildArgs   bool
	}{
		StackInfo:      info,
		PackageManager: pm,
		BuildDir:       buildDir,
		FrameworkName:  frameworkName,
		EnvPrefix:      envPrefix,
		BuildArgs:      buildArgs,
		HasBuildArgs:   len(buildArgs) > 0,
	}

	tmpl := `# Build stage for {{.FrameworkName}}
FROM node:20-alpine AS builder

WORKDIR /app

# Copy package files for layer caching
COPY package*.json ./
{{if eq .PackageManager "pnpm"}}COPY pnpm-lock.yaml ./
RUN npm install -g pnpm && pnpm install --frozen-lockfile
{{else if eq .PackageManager "yarn"}}COPY yarn.lock ./
RUN yarn install --frozen-lockfile
{{else if eq .PackageManager "bun"}}COPY bun.lockb ./
RUN npm install -g bun && bun install --frozen-lockfile
{{else}}RUN npm ci
{{end}}

COPY . .

# Build arguments for environment variables (set at build time)
{{range .BuildArgs}}ARG {{.}}
ENV {{.}}=${{"{"}}{{.}}{{"}"}}
{{end}}

RUN npm run build

# Runtime stage - nginx for static serving
FROM nginx:alpine

# Copy built files
COPY --from=builder /app/{{.BuildDir}} /usr/share/nginx/html

# Custom nginx config for SPA routing with security headers
RUN cat > /etc/nginx/conf.d/default.conf << 'EOF'
server {
    listen 80;
    server_name _;
    root /usr/share/nginx/html;
    index index.html;

    # Security headers
    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-XSS-Protection "1; mode=block" always;

    # Gzip compression
    gzip on;
    gzip_vary on;
    gzip_min_length 1024;
    gzip_types text/plain text/css text/xml text/javascript application/javascript application/json application/xml;

    # Health check endpoint for monitoring
    location /health {
        access_log off;
        return 200 'ok';
        add_header Content-Type text/plain;
    }

    # Cache static assets
    location ~* \.(js|css|png|jpg|jpeg|gif|ico|svg|woff|woff2|ttf|eot)$ {
        expires 1y;
        add_header Cache-Control "public, immutable";
    }

    # SPA routing - serve index.html for all routes
    location / {
        try_files $uri $uri/ /index.html;
    }
}
EOF

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
`
	return executeTemplate(tmpl, data)
}
