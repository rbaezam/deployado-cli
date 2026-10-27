package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/deployado/cli/internal/api"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// doctor is the single command users reach for when an app is acting
// weird. It runs a fixed checklist against the platform's state and
// the app's current health, formatting each result with an actionable
// hint when something is wrong.
//
// The checks are intentionally lightweight (sub-second each) so the
// command is safe to run repeatedly during incident response. Heavy
// diagnostics (full log scan, DB connection tests across the network)
// are gated by --deep.
//
// Each check returns a result; the runner aggregates them at the end
// to produce a "N passed, M warnings, K failed" summary.

var (
	doctorDeep     bool
	doctorJSON     bool
	doctorWatch    bool
	doctorInterval int
)

var doctorCmd = &cobra.Command{
	Use:   "doctor [app-id]",
	Short: "Run diagnostic checks on an app and report what's broken",
	Long: `Runs a fixed checklist against the app and reports each result
with a "what to do" hint when something is wrong.

Checks:
  - App exists in the account
  - Current deployment status (healthy/degraded/failed)
  - Latest build status
  - Target server health (online/offline/degraded)
  - Instance count vs healthy count
  - HTTP probe of the primary domain (3s timeout, 10s with --deep)
  - Recent runtime logs grepped for error-shaped lines
  - Environment variable presence
  - Placement (which server/container is serving)

Flags:
  --deep            Scan more log lines (500 vs 100) and use longer HTTP timeout
  --json            Emit machine-readable JSON instead of the formatted summary
  -w, --watch       Re-run the checklist on an interval (Ctrl-C to exit).
                    Useful during incident response when you want to see
                    state change in real time.
  --interval N      Seconds between iterations in --watch mode (default 5)

Exit codes:
  0  all checks passed (warnings allowed)
  1  one or more checks failed (single-run mode only — watch mode never exits with 1)
  2  the app id could not be resolved`,
	Args: cobra.ExactArgs(1),
	RunE: runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
	doctorCmd.Flags().BoolVar(&doctorDeep, "deep", false,
		"Run slower checks (full log scan, DB connection tests)")
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false,
		"Emit machine-readable JSON instead of the formatted summary")
	doctorCmd.Flags().BoolVarP(&doctorWatch, "watch", "w", false,
		"Re-run the checklist on an interval (Ctrl-C to exit)")
	doctorCmd.Flags().IntVar(&doctorInterval, "interval", 5,
		"Seconds between iterations in --watch mode")
}

// checkStatus is the three-way result every individual check returns.
// Reasoning: a binary pass/fail loses the difference between "this is
// actively broken" and "this looks weird but might be fine"; healthy
// platforms have warnings all the time and you don't want to drown them
// out with red.
type checkStatus int

const (
	statusPass checkStatus = iota
	statusInfo             // contextual, never a problem
	statusWarn             // worth attention, not necessarily an outage
	statusFail             // user-actionable failure
)

// MarshalJSON makes the --json output emit the human-meaningful label
// instead of the underlying iota integer. Without this, reordering the
// const block above would silently break every script that parses
// `deployado doctor --json` — and that's exactly the kind of stable
// contract a doctor command needs to keep.
func (s checkStatus) MarshalJSON() ([]byte, error) {
	var label string
	switch s {
	case statusPass:
		label = "pass"
	case statusInfo:
		label = "info"
	case statusWarn:
		label = "warn"
	case statusFail:
		label = "fail"
	default:
		label = "unknown"
	}
	return []byte(`"` + label + `"`), nil
}

type checkResult struct {
	Name   string      `json:"name"`
	Status checkStatus `json:"status"`
	// Single-line summary shown next to the status glyph.
	Message string `json:"message"`
	// Optional second line — surfaced indented under Message. Use for
	// "what to do" guidance on warnings/failures; skip on passes.
	Hint string `json:"hint,omitempty"`
}

func runDoctor(_ *cobra.Command, args []string) error {
	appID := args[0]

	if doctorWatch {
		// --watch is mutually exclusive with --json: watch mode is a
		// human-readable TUI, JSON consumers should poll the
		// single-run mode themselves with their own cadence. Erroring
		// here is friendlier than silently corrupting their pipeline.
		if doctorJSON {
			return fmt.Errorf("--watch and --json are mutually exclusive (use a polling loop instead)")
		}
		return runDoctorWatch(appID)
	}

	return runDoctorOnce(appID, true)
}

// runDoctorOnce runs the full checklist a single time and prints
// (or emits JSON for) the results. The exitOnFailure switch lets
// the watch loop call this without crashing the process when a
// check fails — watch mode wants to keep ticking through outages,
// not exit at the first one.
func runDoctorOnce(appID string, exitOnFailure bool) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	app, err := client.GetApp(appID)
	if err != nil {
		// Failed before we even started — emit a single failed check
		// and exit so the user sees a structured error instead of a
		// raw HTTP failure.
		printResult(checkResult{
			Name:    "App lookup",
			Status:  statusFail,
			Message: err.Error(),
			Hint:    "Verify the app id with `deployado apps list`.",
		})
		if exitOnFailure {
			os.Exit(2)
		}
		return nil
	}

	results := []checkResult{
		{Name: "App", Status: statusPass,
			Message: fmt.Sprintf("%s (branch=%s, port=%d)", app.Name, app.Branch, app.Port)},
	}

	results = append(results, checkDeployment(client, app))
	results = append(results, checkLatestBuild(client, app))
	results = append(results, checkTargetServer(client, app))
	results = append(results, checkInstanceCount(client, app))
	results = append(results, checkLiveMetrics(client, app))
	results = append(results, checkPrimaryDomain(client, app))
	results = append(results, checkLogs(client, app))
	results = append(results, checkEnvVars(client, app))
	results = append(results, checkPlacement(client, app))

	if doctorJSON {
		return emitJSON(results)
	}

	for _, r := range results {
		printResult(r)
	}
	printSummary(results)

	if exitOnFailure && hasFailures(results) {
		os.Exit(1)
	}
	return nil
}

// runDoctorWatch is the --watch loop. Clears the screen between
// iterations, prints a timestamped header so the user can see the
// last refresh, and never exits on failure (incident response wants
// to watch state change, not bail at the first red row).
//
// Ctrl-C is the exit signal — we don't install a custom handler
// because the loop is a plain time.Sleep and the OS-default SIGINT
// behavior (terminate the process) does the right thing.
func runDoctorWatch(appID string) error {
	interval := doctorInterval
	if interval < 1 {
		interval = 1
	}

	for {
		// Move cursor to top-left and clear the entire screen.
		// Avoids the "every iteration scrolls past the previous one"
		// flicker; the user always sees the current state at the top.
		fmt.Print("\033[H\033[2J")

		color.New(color.FgCyan, color.Bold).Printf(
			"deployado doctor %s — %s (every %ds; Ctrl-C to exit)\n\n",
			appID,
			time.Now().Format("15:04:05"),
			interval,
		)

		// Ignored error: watch never exits on check failure.
		_ = runDoctorOnce(appID, false)

		time.Sleep(time.Duration(interval) * time.Second)
	}
}

// ---------------------------------------------------------------
// Individual checks
// ---------------------------------------------------------------

// checkDeployment looks at the current deployment row. The most
// actionable answer here is "what version is the user actually
// running vs the latest release they pushed".
func checkDeployment(client *api.Client, app *api.App) checkResult {
	dep, err := client.GetCurrentDeployment(app.ID)
	if err != nil {
		return checkResult{
			Name:    "Current deployment",
			Status:  statusFail,
			Message: "couldn't fetch deployment status: " + err.Error(),
			Hint:    "Try `deployado deployments list " + app.ID + "`.",
		}
	}
	if dep == nil {
		return checkResult{
			Name:    "Current deployment",
			Status:  statusFail,
			Message: "no current deployment",
			Hint:    "Trigger one with `deployado deploy " + app.ID + "`.",
		}
	}

	switch dep.Status {
	case "healthy":
		return checkResult{
			Name:   "Current deployment",
			Status: statusPass,
			Message: fmt.Sprintf("v%s healthy %s on %s",
				dep.Version, sinceStr(dep.StartedAt), dep.ServerName),
		}
	case "degraded":
		return checkResult{
			Name:    "Current deployment",
			Status:  statusWarn,
			Message: fmt.Sprintf("v%s DEGRADED on %s", dep.Version, dep.ServerName),
			Hint:    "Container may have stopped — `deployado deploy " + app.ID + "` to restart.",
		}
	case "failed":
		msg := fmt.Sprintf("v%s FAILED", dep.Version)
		if dep.Error != nil && *dep.Error != "" {
			msg += ": " + *dep.Error
		}
		return checkResult{
			Name:    "Current deployment",
			Status:  statusFail,
			Message: msg,
			Hint:    "Check build logs or roll back with `deployado rollback " + app.ID + "`.",
		}
	default:
		return checkResult{
			Name:    "Current deployment",
			Status:  statusInfo,
			Message: fmt.Sprintf("v%s status=%s", dep.Version, dep.Status),
		}
	}
}

// checkLatestBuild surfaces the most-recent release status so a
// stuck-mid-build is visible without having to dig through the dashboard.
func checkLatestBuild(client *api.Client, app *api.App) checkResult {
	releases, err := client.ListReleases(app.ID, 5)
	if err != nil {
		return checkResult{
			Name:    "Latest build",
			Status:  statusWarn,
			Message: "couldn't list releases: " + err.Error(),
		}
	}
	if len(releases) == 0 {
		return checkResult{
			Name:    "Latest build",
			Status:  statusWarn,
			Message: "no releases recorded",
			Hint:    "Trigger your first deploy with `deployado deploy " + app.ID + "`.",
		}
	}

	latest := releases[0]
	switch latest.Status {
	case "ready":
		marker := ""
		if latest.IsCurrent {
			marker = " (current)"
		}
		return checkResult{
			Name:    "Latest build",
			Status:  statusPass,
			Message: fmt.Sprintf("v%s ready%s", latest.Version, marker),
		}
	case "building", "pending":
		return checkResult{
			Name:    "Latest build",
			Status:  statusInfo,
			Message: fmt.Sprintf("v%s %s (in progress)", latest.Version, latest.Status),
			Hint:    "Watch with `deployado deploy " + app.ID + " --watch`.",
		}
	case "failed", "build_failed":
		// Server schema uses "failed" (per Release.@statuses).
		// "build_failed" is kept as a fallback in case the API ever
		// surfaces the more explicit name — costs nothing to accept
		// both, and protects against a silent miss like the one
		// caught by the QueuedBuildDrainerWorker test that exposed
		// this typo on 2026-05-25.
		return checkResult{
			Name:    "Latest build",
			Status:  statusFail,
			Message: fmt.Sprintf("v%s build failed", latest.Version),
			Hint:    "Inspect with `deployado releases logs " + latest.ID + "`.",
		}
	default:
		return checkResult{
			Name:    "Latest build",
			Status:  statusInfo,
			Message: fmt.Sprintf("v%s status=%s", latest.Version, latest.Status),
		}
	}
}

// checkTargetServer surfaces the health of the server the app is
// pinned to. A degraded or offline server is invisible to the user
// from the deployment row alone (deployments report their own
// state, not the server's), and "my deploy worked but the app is
// broken" almost always traces back to the server itself when the
// app is multi-instance and one node is sick.
func checkTargetServer(client *api.Client, app *api.App) checkResult {
	if app.TargetServerID == "" {
		// Auto-placement apps don't pin to a server; skip the check
		// rather than guess which one to probe.
		return checkResult{
			Name:    "Target server",
			Status:  statusInfo,
			Message: "auto-placement (no pinned server)",
		}
	}

	servers, err := client.ListServers()
	if err != nil {
		return checkResult{
			Name:    "Target server",
			Status:  statusInfo,
			Message: "couldn't list servers: " + err.Error(),
		}
	}

	var server *api.Server
	for i := range servers {
		if servers[i].ID == app.TargetServerID {
			server = &servers[i]
			break
		}
	}

	if server == nil {
		return checkResult{
			Name:    "Target server",
			Status:  statusFail,
			Message: "pinned server " + app.TargetServerID[:8] + " not in account's servers",
			Hint:    "The pin may reference a deleted server. Re-assign in the dashboard.",
		}
	}

	switch server.Status {
	case "online":
		return checkResult{
			Name:    "Target server",
			Status:  statusPass,
			Message: fmt.Sprintf("%s online (%s)", server.Name, server.IPAddress),
		}
	case "offline":
		return checkResult{
			Name:    "Target server",
			Status:  statusFail,
			Message: fmt.Sprintf("%s OFFLINE", server.Name),
			Hint:    "Last heartbeat: " + sinceStr(server.LastHeartbeatAt),
		}
	default:
		return checkResult{
			Name:    "Target server",
			Status:  statusWarn,
			Message: fmt.Sprintf("%s status=%s", server.Name, server.Status),
		}
	}
}

// checkInstanceCount answers "how many copies of this app are
// actually running right now". We don't have an "expected" count
// from the API, so the value comes from spotting the abnormal
// shapes — zero current deployments (= app is dark), or all
// current deployments degraded/failed.
func checkInstanceCount(client *api.Client, app *api.App) checkResult {
	deps, err := client.ListDeployments(app.ID, 20, "")
	if err != nil {
		return checkResult{
			Name:    "Instances",
			Status:  statusInfo,
			Message: "couldn't list deployments: " + err.Error(),
		}
	}

	current, healthy, degraded, failed := tallyDeploymentStatuses(deps)
	status, msg, hint := classifyInstanceHealth(current, healthy, degraded, failed, app.ID)

	return checkResult{
		Name:    "Instances",
		Status:  status,
		Message: msg,
		Hint:    hint,
	}
}

// tallyDeploymentStatuses counts how many of the deployments passed
// in are current=true, broken down by their reported status. Returns
// (current, healthy, degraded, failed). Non-current deployments are
// ignored on purpose — they're historical, not part of "what's
// serving traffic right now".
func tallyDeploymentStatuses(deps []api.Deployment) (current, healthy, degraded, failed int) {
	for _, d := range deps {
		if !d.IsCurrent {
			continue
		}
		current++
		switch d.Status {
		case "healthy":
			healthy++
		case "degraded":
			degraded++
		case "failed":
			failed++
		}
	}
	return
}

// classifyInstanceHealth turns a count tuple into a (status, message,
// hint) result. Extracted so we can unit-test the boundaries without
// hitting the network — the priority order (zero → all-healthy →
// all-down → mixed) is easy to get subtly wrong.
func classifyInstanceHealth(current, healthy, degraded, failed int, appID string) (checkStatus, string, string) {
	switch {
	case current == 0:
		return statusFail,
			"no current deployments — app is not serving traffic",
			"Trigger a deploy with `deployado deploy " + appID + "`."
	case healthy == current:
		return statusPass, fmt.Sprintf("%d/%d healthy", healthy, current), ""
	case healthy == 0:
		return statusFail,
			fmt.Sprintf("0/%d healthy (%d degraded, %d failed)", current, degraded, failed),
			"All instances down. Check the target server and recent logs."
	default:
		return statusWarn,
			fmt.Sprintf("%d/%d healthy (degraded: %d, failed: %d)", healthy, current, degraded, failed),
			""
	}
}

// checkLiveMetrics reports the latest container CPU/memory snapshot.
// The MetricsCollectorWorker on the control plane refreshes this
// every minute, so a "stale" reading (>2 min old) is itself
// diagnostic — either the metrics worker is dead or the agent
// isn't responding to stats queries.
//
// Thresholds were chosen to match what the dashboard's alert
// system uses (so doctor and alerting agree on what "high" means):
//
//   - >95% CPU sustained → fail (definitely throttled)
//   - >80% CPU           → warn (close to the wall, investigate)
//   - same for memory
//
// Memory is the harder one: hitting 100% means OOM-kill, not just
// slowdown. We err harder there.
func checkLiveMetrics(client *api.Client, app *api.App) checkResult {
	dep, err := client.GetCurrentDeployment(app.ID)
	if err != nil || dep == nil {
		return checkResult{
			Name:    "Resources (live)",
			Status:  statusInfo,
			Message: "skip (no current deployment)",
		}
	}

	if len(dep.ContainerMetrics) == 0 {
		return checkResult{
			Name:    "Resources (live)",
			Status:  statusInfo,
			Message: "no metrics snapshot yet (first sweep within 1 min of deploy)",
		}
	}

	// Reading from a map[string]any: JSON numbers come back as
	// float64 from encoding/json regardless of how they were
	// serialized server-side. Coerce defensively in case the
	// shape ever shifts.
	cpuPct := asFloat(dep.ContainerMetrics["cpu_percent"])
	memUsed := asFloat(dep.ContainerMetrics["memory_used_mb"])
	memLimit := asFloat(dep.ContainerMetrics["memory_limit_mb"])

	memPct := 0.0
	if memLimit > 0 {
		memPct = (memUsed / memLimit) * 100
	}

	status := statusPass
	hint := ""
	switch {
	case cpuPct > 95 || memPct > 95:
		status = statusFail
		hint = "App is at the resource ceiling — expect throttling (CPU) or OOM (memory). Bump limits in `deployado apps update`."
	case cpuPct > 80 || memPct > 80:
		status = statusWarn
		hint = "Approaching limits. Consider raising or investigating a runaway workload."
	}

	msg := fmt.Sprintf("CPU %.1f%%, memory %.0f/%.0f MB (%.1f%%)",
		cpuPct, memUsed, memLimit, memPct)

	// Surface staleness: if the snapshot is more than 2 minutes old
	// the metrics collector is missing ticks. The CPU/mem values
	// might still be reasonable but we don't want users acting on
	// stale data without knowing.
	if dep.ContainerMetricsUpdatedAt != nil {
		age := sinceStr(*dep.ContainerMetricsUpdatedAt)
		msg += " (" + age + ")"

		if metricsStale(*dep.ContainerMetricsUpdatedAt) {
			// Stale enough that the values shouldn't be trusted.
			// Downgrade pass→info so it doesn't get a green check.
			if status == statusPass {
				status = statusInfo
			}
			hint = "Metrics snapshot is >2 min old — collector may be unhealthy."
		}
	}

	return checkResult{
		Name:    "Resources (live)",
		Status:  status,
		Message: msg,
		Hint:    hint,
	}
}

// asFloat coerces a json.Unmarshal'd value to float64 with a zero
// default. Most numbers from the API arrive as float64 already; the
// other branches are belt-and-suspenders for shape drift.
func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

// metricsStale returns true if the snapshot is older than 2 minutes,
// which means the per-minute MetricsCollectorWorker skipped at least
// one tick. RFC3339-ish input; degrade to "not stale" on parse error
// so we don't false-alarm on a malformed timestamp.
func metricsStale(ts string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, ts)
	}
	if err != nil {
		return false
	}
	return time.Since(parsed) > 2*time.Minute
}

// checkPrimaryDomain HTTP-probes the app's primary domain. This is
// the "is the app actually serving requests" question that no other
// check answers — a deployment can be marked healthy by the agent
// while nginx routes are still 502ing because of an upstream config
// glitch, and only an outside-in probe catches that.
//
// Timeout is intentionally short (3s) so doctor stays sub-2-second
// in the common case where everything is fine. `--deep` raises it
// to 10s for the case where the domain is slow but reachable.
func checkPrimaryDomain(client *api.Client, app *api.App) checkResult {
	domains, err := client.ListDomains(app.ID)
	if err != nil {
		return checkResult{
			Name:    "HTTP probe",
			Status:  statusInfo,
			Message: "couldn't list domains: " + err.Error(),
		}
	}

	var primary *api.Domain
	for i := range domains {
		if domains[i].IsPrimary {
			primary = &domains[i]
			break
		}
	}
	// Fallback: if no primary is marked, use whichever domain is
	// "verified" (or the first one). Better to probe something than
	// silently skip the check.
	if primary == nil {
		for i := range domains {
			if domains[i].Status == "verified" {
				primary = &domains[i]
				break
			}
		}
	}
	if primary == nil && len(domains) > 0 {
		primary = &domains[0]
	}

	if primary == nil {
		return checkResult{
			Name:    "HTTP probe",
			Status:  statusInfo,
			Message: "no domains attached — skipping probe",
		}
	}

	timeout := 3 * time.Second
	if doctorDeep {
		timeout = 10 * time.Second
	}

	client2 := &http.Client{
		Timeout: timeout,
		// Don't follow redirects automatically — a 301/302 IS the
		// response we want to report, and following it can mask a
		// loop or an unrelated server returning the final 200.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	url := "https://" + primary.Hostname
	resp, err := client2.Get(url)
	if err != nil {
		return checkResult{
			Name:    "HTTP probe",
			Status:  statusFail,
			Message: fmt.Sprintf("%s: %s", url, err.Error()),
			Hint:    "App may be down at the edge. Check the target server and Traefik.",
		}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 400:
		return checkResult{
			Name:    "HTTP probe",
			Status:  statusPass,
			Message: fmt.Sprintf("%s → %d", url, resp.StatusCode),
		}
	case resp.StatusCode >= 500:
		return checkResult{
			Name:    "HTTP probe",
			Status:  statusFail,
			Message: fmt.Sprintf("%s → %d (server error)", url, resp.StatusCode),
			Hint:    "5xx from the edge. Likely a crashed container or misconfigured upstream.",
		}
	default:
		// 4xx — could be a legitimate auth gate or a 404 on the root.
		// We report it but don't fail, since plenty of healthy apps
		// return 4xx at "/" by design (APIs, dashboards behind auth).
		return checkResult{
			Name:    "HTTP probe",
			Status:  statusWarn,
			Message: fmt.Sprintf("%s → %d", url, resp.StatusCode),
			Hint:    "4xx at '/' may be expected (auth gate, missing route). Investigate if surprising.",
		}
	}
}

// checkLogs greps the recent runtime logs for common error
// fingerprints. Not a deep scan — just a quick "did the app start
// throwing exceptions" signal so users don't have to mentally parse
// `deployado logs` themselves.
var errorLogPattern = regexp.MustCompile(
	`(?i)(error|exception|panic|fatal|traceback|stack trace|undefined|cannot connect)`)

func checkLogs(client *api.Client, app *api.App) checkResult {
	lines := 100
	if doctorDeep {
		lines = 500
	}

	logs, err := client.GetLogs(app.ID, lines)
	if err != nil {
		return checkResult{
			Name:    "Recent logs",
			Status:  statusInfo,
			Message: "couldn't fetch logs: " + err.Error(),
		}
	}
	if logs == nil || len(logs.Logs) == 0 {
		return checkResult{
			Name:    "Recent logs",
			Status:  statusInfo,
			Message: "no recent log output",
		}
	}

	count := 0
	for _, entry := range logs.Logs {
		if errorLogPattern.MatchString(entry.Message) {
			count++
		}
	}

	total := len(logs.Logs)
	if count == 0 {
		return checkResult{
			Name:    "Recent logs",
			Status:  statusPass,
			Message: fmt.Sprintf("no error-shaped lines in last %d", total),
		}
	}

	status := statusWarn
	hint := fmt.Sprintf("Inspect with `deployado logs %s --lines %d`.", app.ID, lines)
	// > 1 error per 10 lines is a strong "actively broken" signal.
	if count*10 > total {
		status = statusFail
	}

	return checkResult{
		Name:    "Recent logs",
		Status:  status,
		Message: fmt.Sprintf("%d error-shaped line(s) in last %d", count, total),
		Hint:    hint,
	}
}

// checkEnvVars gives a quick count — useful sanity check for "did
// the env vars get cleared somehow" or "wait, I never set DATABASE_URL".
// Not a validation, just a presence signal.
func checkEnvVars(client *api.Client, app *api.App) checkResult {
	env, err := client.GetEnv(app.ID)
	if err != nil {
		return checkResult{
			Name:    "Environment",
			Status:  statusInfo,
			Message: "couldn't fetch env: " + err.Error(),
		}
	}

	total := len(env)
	if total == 0 {
		return checkResult{
			Name:    "Environment",
			Status:  statusWarn,
			Message: "no env vars set",
			Hint:    "Most apps need at least DATABASE_URL or similar.",
		}
	}

	return checkResult{
		Name:    "Environment",
		Status:  statusInfo,
		Message: fmt.Sprintf("%d var(s) set", total),
	}
}

// checkPlacement reports where the app's current deployment is
// running. Mostly a sanity signal: if a user expects pinning to
// app-server-1 and we show them server-3, that's the lead.
// (Resource reservation / live usage live in the dashboard's metrics
// tab — surfacing them here would require an extra endpoint that
// doesn't exist yet.)
func checkPlacement(client *api.Client, app *api.App) checkResult {
	dep, err := client.GetCurrentDeployment(app.ID)
	if err != nil || dep == nil {
		return checkResult{
			Name:    "Placement",
			Status:  statusInfo,
			Message: "skip (no current deployment)",
		}
	}

	containerInfo := "no container id"
	if dep.ContainerID != nil && *dep.ContainerID != "" {
		short := *dep.ContainerID
		if len(short) > 12 {
			short = short[:12]
		}
		containerInfo = "container " + short
	}

	return checkResult{
		Name:    "Placement",
		Status:  statusInfo,
		Message: fmt.Sprintf("on %s, %s", dep.ServerName, containerInfo),
		// (No hint: the new "Resources (live)" check above already
		// shows real CPU/memory numbers. The old "see dashboard"
		// deflection was the symptom this whole check was trying to
		// avoid — removing it now that the answer is right there.)
	}
}

// ---------------------------------------------------------------
// Output formatting
// ---------------------------------------------------------------

func printResult(r checkResult) {
	var glyph string
	var c *color.Color

	switch r.Status {
	case statusPass:
		glyph = "✓"
		c = color.New(color.FgGreen)
	case statusInfo:
		glyph = "ⓘ"
		c = color.New(color.FgCyan)
	case statusWarn:
		glyph = "⚠"
		c = color.New(color.FgYellow)
	case statusFail:
		glyph = "✗"
		c = color.New(color.FgRed, color.Bold)
	}

	c.Printf("  %s  %-22s ", glyph, r.Name)
	fmt.Println(r.Message)
	if r.Hint != "" {
		color.New(color.FgWhite, color.Faint).Printf("     %s\n", r.Hint)
	}
}

func printSummary(results []checkResult) {
	var pass, warn, fail int
	for _, r := range results {
		switch r.Status {
		case statusPass:
			pass++
		case statusWarn:
			warn++
		case statusFail:
			fail++
		}
	}

	fmt.Println()
	switch {
	case fail > 0:
		color.New(color.FgRed, color.Bold).Printf("Summary: %d passed, %d warning, %d failed\n",
			pass, warn, fail)
	case warn > 0:
		color.New(color.FgYellow).Printf("Summary: %d passed, %d warning, %d failed\n",
			pass, warn, fail)
	default:
		color.New(color.FgGreen).Printf("Summary: %d passed, %d warning, %d failed\n",
			pass, warn, fail)
	}
}

func emitJSON(results []checkResult) error {
	// Use the standard library's encoder so consumers don't need to
	// guess our color/glyph format. Wraps in {"checks": [...]} so we
	// can add top-level fields later without breaking parsers.
	out, err := json.MarshalIndent(map[string]any{"checks": results}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

func hasFailures(results []checkResult) bool {
	for _, r := range results {
		if r.Status == statusFail {
			return true
		}
	}
	return false
}

// sinceStr returns a short human-readable age like "11m" or "2h".
// RFC3339-ish input from the API; failed parses degrade to "recently".
func sinceStr(t string) string {
	parsed, err := time.Parse(time.RFC3339Nano, t)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, t)
	}
	if err != nil {
		return "recently"
	}
	d := time.Since(parsed)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dmin ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
