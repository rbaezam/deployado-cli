package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is the Deployado API client
type Client struct {
	host       string
	token      string
	httpClient *http.Client
}

// NewClient creates a new API client
func NewClient(host, token string) *Client {
	return &Client{
		host:  host,
		token: token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Error represents an API error response
type Error struct {
	Code        string                 `json:"error"`
	Description string                 `json:"error_description"`
	Message     string                 `json:"message"`
	Details     map[string]interface{} `json:"details"`
}

func (e *Error) Error() string {
	// Validation-style errors should ALWAYS surface the per-field detail
	// — that's where the actionable info lives ("key must match regex
	// ...", "scope must be one of: runtime, build"). Before this change,
	// the CLI just printed the generic `message` ("Failed to update
	// environment variables") and silently dropped the changeset
	// details, leaving users guessing what was wrong.
	if details := formatValidationDetails(e.Details); details != "" {
		switch {
		case e.Description != "":
			return fmt.Sprintf("%s — %s", e.Description, details)
		case e.Message != "":
			return fmt.Sprintf("%s — %s", e.Message, details)
		default:
			return fmt.Sprintf("%s (%s)", e.Code, details)
		}
	}

	if e.Description != "" {
		return e.Description
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// formatValidationDetails turns a `details` map from a server error
// response into a human-readable string. Returns "" when the map is
// empty or has no recognisable per-field errors.
func formatValidationDetails(details map[string]interface{}) string {
	if len(details) == 0 {
		return ""
	}

	var parts []string
	for field, errs := range details {
		switch v := errs.(type) {
		case []interface{}:
			for _, err := range v {
				parts = append(parts, fmt.Sprintf("%s: %v", field, err))
			}
		case string:
			parts = append(parts, fmt.Sprintf("%s: %s", field, v))
		default:
			// Skip non-validation shapes (e.g. an "inspect dump" reason
			// blob — providing it would just be noise).
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

// DeviceCodeResponse is the response from POST /api/cli/device
type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// TokenResponse is the response from POST /api/cli/token
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// MeResponse is the response from GET /api/v1/me
type MeResponse struct {
	Account struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"account"`
	User struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	} `json:"user"`
	Token struct {
		Name       string `json:"name"`
		LastUsedAt string `json:"last_used_at"`
		ExpiresAt  string `json:"expires_at"`
	} `json:"token"`
}

// App represents an application
type App struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Slug                string `json:"slug"`
	ProjectID           string `json:"project_id"`
	RepositoryURL       string `json:"repository_url"`
	Branch              string `json:"branch"`
	Port                int    `json:"port"`
	DockerfilePath      string `json:"dockerfile_path"`
	BuildContext        string `json:"build_context"`
	TargetServerID      string `json:"target_server_id"`
	AutoDeployEnabled   bool   `json:"auto_deploy_enabled"`
	DeployGateEnabled   bool   `json:"deploy_gate_enabled"`
	AutoRollbackEnabled bool   `json:"auto_rollback_enabled"`
}

// AppsResponse is the response from GET /api/v1/apps
type AppsResponse struct {
	Apps []App `json:"apps"`
}

// AppResponse is the response from GET /api/v1/apps/:id
type AppResponse struct {
	App App `json:"app"`
}

// Domain represents a custom domain attached to an app
type Domain struct {
	ID         string `json:"id"`
	Hostname   string `json:"hostname"`
	Status     string `json:"status"`
	IsPrimary  bool   `json:"is_primary"`
	InsertedAt string `json:"inserted_at"`
}

// DomainsResponse is the response from GET /api/v1/apps/:id/domains
type DomainsResponse struct {
	Domains []Domain `json:"domains"`
}

// Project represents a project
type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

// ProjectsResponse is the response from GET /api/v1/projects
type ProjectsResponse struct {
	Projects []Project `json:"projects"`
}

// Server represents a server
type Server struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Hostname        string             `json:"hostname"`
	IPAddress       string             `json:"ip_address"`
	Status          string             `json:"status"`
	Roles           []string           `json:"roles"`
	LastHeartbeatAt string             `json:"last_heartbeat_at"`
	Metrics         map[string]float64 `json:"metrics"`
}

// ServersResponse is the response from GET /api/v1/servers
type ServersResponse struct {
	Servers []Server `json:"servers"`
}

// EnvResponse is the response from GET /api/v1/apps/:id/env
type EnvResponse struct {
	Env map[string]string `json:"env"`
}

// DeployResponse is the response from POST /api/v1/apps/:id/deploy
type DeployResponse struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	ReleaseID string `json:"release_id"`
}

// RollbackResponse is the response from POST /api/v1/apps/:id/rollback
type RollbackResponse struct {
	OK             bool   `json:"ok"`
	Message        string `json:"message"`
	DeploymentID   string `json:"deployment_id"`
	ReleaseVersion string `json:"release_version"`
}

// DBQueryResponse is the response from POST /api/v1/apps/:id/db/query
type DBQueryResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// QueryAppDB runs one-shot SQL against an app's database (via the DB
// server's agent) and returns the buffered psql output.
func (c *Client) QueryAppDB(appID, sql string, readonly, asJSON bool) (*DBQueryResponse, error) {
	body := map[string]interface{}{"sql": sql, "readonly": readonly, "json": asJSON}

	resp, err := c.post("/api/v1/apps/"+appID+"/db/query", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DBQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// DBRestoreResponse is the response from POST /api/v1/apps/:id/db/restore
type DBRestoreResponse struct {
	OK           bool   `json:"ok"`
	Message      string `json:"message"`
	RestoreRunID string `json:"restore_run_id"`
}

// RestoreAppDB restores an app's database to a release version or snapshot id.
func (c *Client) RestoreAppDB(appID, to string, dropFirst bool) (*DBRestoreResponse, error) {
	body := map[string]interface{}{"to": to, "drop_first": dropFirst}

	resp, err := c.post("/api/v1/apps/"+appID+"/db/restore", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DBRestoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// Release represents a release version
type Release struct {
	ID            string `json:"id"`
	Version       string `json:"version"`
	Status        string `json:"status"`
	CommitSHA     string `json:"commit_sha"`
	CommitMessage string `json:"commit_message"`
	CreatedAt     string `json:"created_at"`
	IsCurrent     bool   `json:"is_current"`
}

// ReleasesResponse is the response from GET /api/v1/apps/:id/releases
type ReleasesResponse struct {
	Releases []Release `json:"releases"`
}

// Deployment represents a deployment
type Deployment struct {
	ID            string  `json:"id"`
	Status        string  `json:"status"`
	Version       string  `json:"version"`
	ServerName    string  `json:"server_name"`
	StartedAt     string  `json:"started_at"`
	FinishedAt    *string `json:"finished_at"`
	TriggeredBy   string  `json:"triggered_by"`
	IsCurrent     bool    `json:"is_current"`
	Error         *string `json:"error"`
	FailureReason *string `json:"failure_reason"`
	ContainerID   *string `json:"container_id"`
	IsRollback    bool    `json:"is_rollback"`

	// Latest container metrics snapshot, refreshed every minute by
	// MetricsCollectorWorker on the control plane. Only populated on
	// detail endpoints (current/show) — list endpoints omit them to
	// keep the payload light. Empty map when no snapshot has been
	// recorded yet (typical right after a fresh deploy).
	ContainerMetrics          map[string]any `json:"container_metrics,omitempty"`
	ContainerMetricsUpdatedAt *string        `json:"container_metrics_updated_at,omitempty"`
}

// DeploymentsResponse is the response from GET /api/v1/apps/:id/deployments
type DeploymentsResponse struct {
	Deployments []Deployment `json:"deployments"`
}

// DeploymentResponse is the response from GET /api/v1/apps/:id/deployments/:id
type DeploymentResponse struct {
	Deployment Deployment `json:"deployment"`
}

// LogEntry represents a single log line
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
	Stream    string `json:"stream"`
}

// LogsResponse is the response from GET /api/v1/apps/:id/logs
type LogsResponse struct {
	Logs    []LogEntry `json:"logs"`
	Message string     `json:"message"`
}

// Database represents a managed PostgreSQL database
type Database struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DBName          string `json:"db_name"`
	DBUser          string `json:"db_user"`
	DBHost          string `json:"db_host"`
	DBPort          int    `json:"db_port"`
	DBVersion       string `json:"db_version"`
	Status          string `json:"status"`
	ServerID        string `json:"server_id"`
	ServerName      string `json:"server_name"`
	ProjectID       string `json:"project_id"`
	ProjectName     string `json:"project_name"`
	ConnectionCount int    `json:"connection_count"`
	ConnectionURL   string `json:"connection_url"`
	BackupEnabled   bool   `json:"backup_enabled"`
	MaxConnections  int    `json:"max_connections"`
	SSLEnabled      bool   `json:"ssl_enabled"`
	ErrorMessage    string `json:"error_message"`
	CreatedAt       string `json:"created_at"`
}

// DatabasesResponse is the response from GET /api/v1/databases
type DatabasesResponse struct {
	Databases []Database `json:"databases"`
}

// DatabaseResponse is the response from GET /api/v1/databases/:id
type DatabaseResponse struct {
	Database Database `json:"database"`
}

// DatabaseConnection represents a link between an app and a database
type DatabaseConnection struct {
	ID            string `json:"id"`
	DatabaseID    string `json:"database_id"`
	DatabaseName  string `json:"database_name"`
	AppID         string `json:"app_id"`
	AppName       string `json:"app_name"`
	EnvVarName    string `json:"env_var_name"`
	ConnectionURL string `json:"connection_url"`
}

// DatabaseConnectionResponse is the response from POST /api/v1/databases/:id/connect
type DatabaseConnectionResponse struct {
	Connection DatabaseConnection `json:"connection"`
}

// DBServer represents a server available for database hosting
type DBServer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IPAddress string `json:"ip_address"`
	Status    string `json:"status"`
}

// DBServersResponse is the response from GET /api/v1/databases/servers
type DBServersResponse struct {
	Servers []DBServer `json:"servers"`
}

// RequestDeviceCode initiates the device authorization flow
func (c *Client) RequestDeviceCode() (*DeviceCodeResponse, error) {
	resp, err := c.post("/api/cli/device", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// PollDeviceToken polls for the access token
func (c *Client) PollDeviceToken(deviceCode string) (*TokenResponse, error) {
	body := map[string]string{
		"device_code": deviceCode,
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
	}

	resp, err := c.post("/api/cli/token", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetMe returns the current user and account info
func (c *Client) GetMe() (*MeResponse, error) {
	resp, err := c.get("/api/v1/me")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result MeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// ListApps returns all apps
func (c *Client) ListApps() ([]App, error) {
	return c.ListAppsByProject("")
}

// ListAppsByProject returns apps, optionally filtered by project ID
func (c *Client) ListAppsByProject(projectID string) ([]App, error) {
	endpoint := "/api/v1/apps"
	if strings.TrimSpace(projectID) != "" {
		endpoint = endpoint + "?project_id=" + url.QueryEscape(projectID)
	}

	resp, err := c.get(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result AppsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Apps, nil
}

// GetApp returns a single app
func (c *Client) GetApp(id string) (*App, error) {
	resp, err := c.get("/api/v1/apps/" + id)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result AppResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result.App, nil
}

// CreateApp creates a new app
func (c *Client) CreateApp(params map[string]interface{}) (*App, error) {
	resp, err := c.post("/api/v1/apps", params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result AppResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result.App, nil
}

// UpdateApp updates an app
func (c *Client) UpdateApp(id string, params map[string]interface{}) (*App, error) {
	resp, err := c.put("/api/v1/apps/"+id, params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result AppResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result.App, nil
}

// ListDomains returns domains for an app
func (c *Client) ListDomains(appID string) ([]Domain, error) {
	resp, err := c.get("/api/v1/apps/" + appID + "/domains")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DomainsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Domains, nil
}

// DeleteApp deletes an app
func (c *Client) DeleteApp(id string) error {
	resp, err := c.delete("/api/v1/apps/" + id)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return parseError(resp)
	}

	return nil
}

// DeployApp triggers a deployment
func (c *Client) DeployApp(id string, params map[string]interface{}) (*DeployResponse, error) {
	resp, err := c.post("/api/v1/apps/"+id+"/deploy", params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DeployResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// RollbackApp triggers a rollback for an app
func (c *Client) RollbackApp(id string, params map[string]interface{}) (*RollbackResponse, error) {
	resp, err := c.post("/api/v1/apps/"+id+"/rollback", params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result RollbackResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// ListReleases returns available releases for an app
func (c *Client) ListReleases(appID string, limit int) ([]Release, error) {
	url := fmt.Sprintf("/api/v1/apps/%s/releases?limit=%d", appID, limit)
	resp, err := c.get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result ReleasesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Releases, nil
}

// ListProjects returns all projects
func (c *Client) ListProjects() ([]Project, error) {
	resp, err := c.get("/api/v1/projects")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result ProjectsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Projects, nil
}

// ListServers returns all servers
func (c *Client) ListServers() ([]Server, error) {
	resp, err := c.get("/api/v1/servers")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result ServersResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Servers, nil
}

// GetEnv returns environment variables for an app
func (c *Client) GetEnv(appID string) (map[string]string, error) {
	resp, err := c.get("/api/v1/apps/" + appID + "/env")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result EnvResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Env, nil
}

// SetEnv sets environment variables for an app
func (c *Client) SetEnv(appID string, env map[string]string) error {
	return c.SetEnvWithScope(appID, env, "")
}

// SetEnvWithScope sets environment variables for an app with an optional scope.
// scope can be "runtime" or "build"; empty means API default.
func (c *Client) SetEnvWithScope(appID string, env map[string]string, scope string) error {
	body := map[string]interface{}{
		"env": env,
	}
	if scope != "" {
		body["scope"] = scope
	}

	resp, err := c.put("/api/v1/apps/"+appID+"/env", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return parseError(resp)
	}

	return nil
}

// DeleteEnv deletes an environment variable
func (c *Client) DeleteEnv(appID, key string) error {
	resp, err := c.delete("/api/v1/apps/" + appID + "/env/" + key)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return parseError(resp)
	}

	return nil
}

// ListDeployments returns recent deployments for an app.
// If releaseID is non-empty, results are filtered to that release.
func (c *Client) ListDeployments(appID string, limit int, releaseID string) ([]Deployment, error) {
	url := fmt.Sprintf("/api/v1/apps/%s/deployments?limit=%d", appID, limit)
	if releaseID != "" {
		url += "&release_id=" + releaseID
	}
	resp, err := c.get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DeploymentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Deployments, nil
}

// GetDeployment returns a specific deployment
func (c *Client) GetDeployment(appID, deploymentID string) (*Deployment, error) {
	url := fmt.Sprintf("/api/v1/apps/%s/deployments/%s", appID, deploymentID)
	resp, err := c.get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DeploymentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result.Deployment, nil
}

// GetCurrentDeployment returns the current deployment for an app
func (c *Client) GetCurrentDeployment(appID string) (*Deployment, error) {
	url := fmt.Sprintf("/api/v1/apps/%s/deployments/current", appID)
	resp, err := c.get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // No current deployment
	}

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DeploymentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result.Deployment, nil
}

// GetLogs returns logs for an app
func (c *Client) GetLogs(appID string, lines int) (*LogsResponse, error) {
	url := fmt.Sprintf("/api/v1/apps/%s/logs?lines=%d", appID, lines)
	resp, err := c.get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result LogsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// BuildLogsResponse mirrors the control plane's
// /apps/:id/releases/:rid/logs payload. The `logs` field is a single
// concatenated string of docker build output (NOT a list of entries
// like LogsResponse) — that's because the build pipeline writes
// straight to stdout/stderr and we serve back the buffered tail.
//
// For in-progress builds, `logs` contains the live partial output
// and `status` is "building"; for finished ones, it's the
// consolidated final transcript. Callers can drive a streaming
// experience by polling and printing the suffix beyond what they've
// already seen.
type BuildLogsResponse struct {
	Logs            string  `json:"logs"`
	Status          string  `json:"status"`
	DurationSeconds *int    `json:"duration_seconds"`
	ErrorMessage    *string `json:"error_message"`
	StartedAt       *string `json:"started_at"`
	FinishedAt      *string `json:"finished_at"`
}

// GetBuildLogs returns the docker build output (or the live partial
// output, if the build is still running) for one release. Used by
// `deployado deploy --watch` to tail builds in real time.
func (c *Client) GetBuildLogs(appID, releaseID string) (*BuildLogsResponse, error) {
	url := fmt.Sprintf("/api/v1/apps/%s/releases/%s/logs", appID, releaseID)
	resp, err := c.get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result BuildLogsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetBuildLogsByRelease is the same as GetBuildLogs but hits the
// cross-app endpoint, so callers that only know the release id (e.g.
// `deployado releases logs <id>`, often invoked from doctor hints)
// don't need to first resolve the app. Account scoping is identical
// — both endpoints look up the release through the user's accounts.
func (c *Client) GetBuildLogsByRelease(releaseID string) (*BuildLogsResponse, error) {
	url := fmt.Sprintf("/api/v1/releases/%s/logs", releaseID)
	resp, err := c.get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result BuildLogsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ExecResult represents the result of a remote command execution
type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// ExecCommand executes a command in an app's running container via the control plane API.
func (c *Client) ExecCommand(appID, command, workingDir string) (*ExecResult, error) {
	body := map[string]interface{}{
		"command": command,
	}
	if workingDir != "" {
		body["working_dir"] = workingDir
	}

	// Use a longer timeout for exec commands
	origTimeout := c.httpClient.Timeout
	c.httpClient.Timeout = 65 * time.Second
	defer func() { c.httpClient.Timeout = origTimeout }()

	resp, err := c.post("/api/v1/apps/"+appID+"/exec", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result ExecResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// ListDatabases returns all databases for the account
func (c *Client) ListDatabases(projectID string) ([]Database, error) {
	url := "/api/v1/databases"
	if projectID != "" {
		url = fmt.Sprintf("/api/v1/databases?project_id=%s", projectID)
	}

	resp, err := c.get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DatabasesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Databases, nil
}

// GetDatabase returns a single database by ID
func (c *Client) GetDatabase(id string) (*Database, error) {
	resp, err := c.get("/api/v1/databases/" + id)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DatabaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result.Database, nil
}

// CreateDatabase creates a new database
func (c *Client) CreateDatabase(params map[string]interface{}) (*Database, error) {
	resp, err := c.post("/api/v1/databases", params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DatabaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result.Database, nil
}

// DeleteDatabase deletes a database
func (c *Client) DeleteDatabase(id string) error {
	resp, err := c.delete("/api/v1/databases/" + id)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return parseError(resp)
	}

	return nil
}

// ConnectDatabase connects an app to a database
func (c *Client) ConnectDatabase(databaseID string, params map[string]interface{}) (*DatabaseConnection, error) {
	resp, err := c.post("/api/v1/databases/"+databaseID+"/connect", params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DatabaseConnectionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result.Connection, nil
}

// DisconnectDatabase disconnects an app from a database
func (c *Client) DisconnectDatabase(databaseID, connectionID string) error {
	resp, err := c.delete("/api/v1/databases/" + databaseID + "/connect/" + connectionID)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return parseError(resp)
	}

	return nil
}

// ListDBServers returns servers available for database hosting
func (c *Client) ListDBServers() ([]DBServer, error) {
	resp, err := c.get("/api/v1/databases/servers")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result DBServersResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Servers, nil
}

// HTTP helpers

func (c *Client) get(path string) (*http.Response, error) {
	return c.request("GET", path, nil)
}

func (c *Client) post(path string, body interface{}) (*http.Response, error) {
	return c.request("POST", path, body)
}

func (c *Client) put(path string, body interface{}) (*http.Response, error) {
	return c.request("PUT", path, body)
}

func (c *Client) delete(path string) (*http.Response, error) {
	return c.request("DELETE", path, nil)
}

// request is the single chokepoint every typed method goes through.
// On a 429 it transparently retries up to `maxRetries` times, honoring
// the server's Retry-After header (capped). On any other status it
// returns the response untouched so the caller's parseError flow runs
// normally.
//
// Why centralize the retry here, not at each call site:
//
//   - There are 40+ typed methods (ListApps, GetLogs, etc.); duplicating
//     retry logic at each one is mistake-prone.
//   - The watcher's phase-2 loop used to die on the first 429
//     because ListDeployments bubbled the error up as fatal. Putting
//     the retry below ListDeployments fixes that without touching the
//     loop at all.
//   - Doctor's 9-call burst can hit a transient burst-bucket limit on
//     Traefik; one retry usually slides under the average back to OK.
//
// Why bound the retries (and not retry forever):
//
//   - A misbehaving server returning 429 indefinitely would hang the
//     CLI. Bounded retries surface the error to the user instead.
//   - Long-polling tools like the watcher would compound the problem
//     by re-queuing more retries every 2-3s.
const (
	maxRetries        = 3
	maxRetryAfterSecs = 30
)

func (c *Client) request(method, path string, body interface{}) (*http.Response, error) {
	url := c.host + path

	// Marshal once outside the loop — bytes.NewReader gives us a fresh
	// readable cursor per attempt without re-allocating the body.
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}

	var lastResp *http.Response
	for attempt := 0; attempt < maxRetries; attempt++ {
		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequest(method, url, bodyReader)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "deployado-cli/1.0")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		// 429: drain + close the body so we don't leak the
		// connection (HTTP/1.1 keep-alive needs the body read out
		// before the next request on the same conn), then wait the
		// suggested duration and retry.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		lastResp = resp

		// Final attempt: don't sleep, just bubble the 429 up so the
		// caller's parseError flow surfaces a clean message.
		if attempt == maxRetries-1 {
			break
		}

		wait := parseRetryAfter(resp.Header.Get("Retry-After"), attempt)
		time.Sleep(wait)
	}

	// All retries exhausted. Return the last 429 so parseError can
	// build a "Too Many Requests" error message — that's clearer than
	// a synthetic "max retries exceeded" string.
	if lastResp != nil {
		// Re-issue ONCE to get a fresh, unclosed Body for parseError.
		// (We drained the original Body on each loop iteration so its
		// reader is at EOF.) Cheaper than caching the body bytes for a
		// path that only fires when the server is sustained-throttling.
		return c.singleAttempt(method, url, bodyBytes)
	}
	return nil, fmt.Errorf("request failed after %d retries", maxRetries)
}

// singleAttempt is the no-retry fallback used to materialize a final
// response with a readable Body after the retry loop exhausted.
func (c *Client) singleAttempt(method, url string, bodyBytes []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if bodyBytes != nil {
		bodyReader = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "deployado-cli/1.0")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.httpClient.Do(req)
}

// parseRetryAfter turns the Retry-After response header into a wait
// duration. The RFC permits two formats:
//
//   - integer seconds:  "Retry-After: 5"
//   - HTTP-date:        "Retry-After: Wed, 21 Oct 2015 07:28:00 GMT"
//
// Traefik and our Phoenix plug both emit integer seconds, so that's
// the common path. Caps the wait at maxRetryAfterSecs to bound the
// blast radius if a server suggests an absurd delay.
//
// `attempt` (zero-indexed) is used for exponential fallback when the
// header is missing/unparseable: 1s, 2s, 4s.
func parseRetryAfter(header string, attempt int) time.Duration {
	if header != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs >= 0 {
			capped := secs
			if capped > maxRetryAfterSecs {
				capped = maxRetryAfterSecs
			}
			return time.Duration(capped) * time.Second
		}
		if t, err := http.ParseTime(header); err == nil {
			d := time.Until(t)
			if d < 0 {
				d = time.Second
			}
			if d > time.Duration(maxRetryAfterSecs)*time.Second {
				d = time.Duration(maxRetryAfterSecs) * time.Second
			}
			return d
		}
	}
	// No / bad header → 1s, 2s, 4s exponential
	d := time.Duration(1<<attempt) * time.Second
	if d > time.Duration(maxRetryAfterSecs)*time.Second {
		d = time.Duration(maxRetryAfterSecs) * time.Second
	}
	return d
}

// ===========================================================================
// Database imports
// ===========================================================================

// DatabaseImport represents an in-flight or completed SQL dump import.
type DatabaseImport struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	FileName       string `json:"file_name"`
	FileSizeBytes  int64  `json:"file_size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	Format         string `json:"format"`
	DropFirst      bool   `json:"drop_first"`
	ErrorMessage   string `json:"error_message"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
	AgentActionID  string `json:"agent_action_id"`
	InsertedAt     string `json:"inserted_at"`
}

type databaseImportResponse struct {
	Import DatabaseImport `json:"import"`
}

// ImportDatabaseDump uploads a SQL dump and triggers the restore action.
// The reader is streamed; large files don't get buffered in memory.
// progress, if non-nil, is invoked periodically with bytes written.
func (c *Client) ImportDatabaseDump(
	databaseID, fileName string,
	body io.Reader,
	contentLength int64,
	opts map[string]string,
	progress func(written int64),
) (*DatabaseImport, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	// Write parts in a goroutine so the request body streams as we encode.
	go func() {
		defer pw.Close()
		defer mw.Close()

		for k, v := range opts {
			if err := mw.WriteField(k, v); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}

		fileHeader := textproto.MIMEHeader{}
		fileHeader.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="file"; filename=%q`, fileName))
		fileHeader.Set("Content-Type", "application/octet-stream")

		part, err := mw.CreatePart(fileHeader)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}

		var src io.Reader = body
		if progress != nil {
			src = &progressReader{r: body, cb: progress}
		}

		if _, err := io.Copy(part, src); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
	}()

	url := c.host + "/api/v1/databases/" + databaseID + "/imports"
	req, err := http.NewRequest("POST", url, pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "deployado-cli/1.0")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// Hint to the server about expected size — useful for proxies that buffer.
	if contentLength > 0 {
		req.ContentLength = -1 // we don't know the multipart-wrapped size exactly
	}

	// Imports can take minutes for large files; relax the client timeout.
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result databaseImportResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result.Import, nil
}

// GetDatabaseImport fetches the latest status of an import.
func (c *Client) GetDatabaseImport(databaseID, importID string) (*DatabaseImport, error) {
	resp, err := c.get(fmt.Sprintf("/api/v1/databases/%s/imports/%s", databaseID, importID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result databaseImportResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result.Import, nil
}

// progressReader wraps an io.Reader to invoke a callback as bytes flow through.
type progressReader struct {
	r       io.Reader
	cb      func(int64)
	written int64
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.written += int64(n)
		p.cb(p.written)
	}
	return n, err
}

func parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	var apiErr Error
	if err := json.Unmarshal(body, &apiErr); err != nil {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	if apiErr.Code == "" && apiErr.Message == "" {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return &apiErr
}
