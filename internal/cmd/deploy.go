package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/deployado/cli/internal/api"
	"github.com/deployado/cli/internal/git"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy [app-id]",
	Short: "Deploy an app",
	Long: `Trigger a deployment for an existing app.

If no app-id is provided, the CLI will try to detect the app
based on the current directory.

Use --watch to follow the deployment progress until it completes.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDeploy,
}

func init() {
	deployCmd.Flags().StringP("branch", "b", "", "Branch to deploy")
	deployCmd.Flags().StringP("commit", "c", "", "Specific commit SHA to deploy")
	deployCmd.Flags().BoolP("watch", "w", false, "Watch deployment progress until completion")
	// --no-build-logs is an escape hatch. The watcher streams build
	// output by default because that's overwhelmingly what users want
	// from --watch — but in CI logs it can be noisy, so let users opt
	// back to status-only output.
	deployCmd.Flags().Bool("no-build-logs", false,
		"Don't stream the docker build output while watching (status updates only)")
	// Runtime logs serve the same "show me what's happening" purpose
	// during phase 2 (the container is booting). Default on for the
	// same reason; --no-runtime-logs for the same opt-out.
	deployCmd.Flags().Bool("no-runtime-logs", false,
		"Don't tail the container's stdout/stderr during rollout (status updates only)")
}

func runDeploy(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	var appID string
	var app *api.App

	if len(args) > 0 {
		appID = args[0]
		// Get app info
		app, err = client.GetApp(appID)
		if err != nil {
			return fmt.Errorf("failed to get app: %w", err)
		}
	} else {
		// Try to detect app from current directory
		app, err = detectAppFromDirectory(client)
		if err != nil {
			return err
		}
		appID = app.Slug
		color.New(color.FgCyan).Printf("Detected app: %s\n", app.Name)
	}

	branch, _ := cmd.Flags().GetString("branch")
	commit, _ := cmd.Flags().GetString("commit")
	watch, _ := cmd.Flags().GetBool("watch")

	params := map[string]interface{}{}
	if branch != "" {
		params["branch"] = branch
	}
	if commit != "" {
		params["commit_sha"] = commit
	}

	fmt.Printf("Deploying %s...\n", app.Name)

	resp, err := client.DeployApp(appID, params)
	if err != nil {
		return fmt.Errorf("failed to deploy: %w", err)
	}

	color.New(color.FgGreen, color.Bold).Println("Deployment started!")
	fmt.Printf("Release ID: %s\n", resp.ReleaseID)

	if watch {
		noBuildLogs, _ := cmd.Flags().GetBool("no-build-logs")
		noRuntimeLogs, _ := cmd.Flags().GetBool("no-runtime-logs")
		fmt.Println("\nWatching deployment progress...")
		return watchDeployment(appID, resp.ReleaseID, !noBuildLogs, !noRuntimeLogs)
	}

	fmt.Println("\nView deployment progress with:")
	fmt.Printf("  deployado deploy %s --watch\n", appID)

	return nil
}

// watchPollInterval is how often each phase of the watcher hits the
// control plane for status. Originally 2s, raised to 3s after a
// Reservento user hit Traefik's 100 req/min/IP rate limit while
// running multiple watchers + doctor in parallel. At 3s, a single
// watcher does ~40 RPM (vs ~60 at 2s) which leaves real headroom
// for parallel sessions. The retry layer in api.Client handles any
// 429 that still slips through, but spending less budget per
// watcher is the cheaper fix.
const watchPollInterval = 3 * time.Second

func watchDeployment(appID string, releaseID string, streamBuildLogs bool, streamRuntimeLogs bool) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinIdx := 0

	// Phase 1 — wait for the build to finish.
	//
	// The control plane creates the deployment row AFTER the image is
	// built and pushed to the registry. With a 3-minute build that
	// means the previous version of this watcher showed
	// "Waiting for deployment to start..." for the full build window
	// with no signal that anything was happening. Users would Ctrl+C
	// thinking the deploy was stuck (it wasn't — but the UX implied
	// it was). Now we poll the release first and surface its status.
	//
	// When streamBuildLogs is true, once the build starts we also tail
	// the docker output the way a local `docker build` would — that's
	// almost always what users want from --watch, since "stuck building"
	// is the most common reason they're watching in the first place.
	// Callers that don't have a release to watch (e.g. rollback, which
	// reuses an existing image) pass an empty releaseID. Skip phase 1
	// entirely in that case — there's no build to monitor.
	if releaseID == "" {
		goto deploymentPhase
	}

	{
	lastReleaseStatus := ""
	printedBuildLogBytes := 0 // tail-style cursor into the build log buffer
	buildLogStreamStarted := false

	for {
		release, err := findRelease(client, appID, releaseID)
		if err != nil {
			// Don't fail the whole watch on a transient list error;
			// just keep spinning and retry next tick.
			fmt.Printf("\r%s Querying release status...", spinner[spinIdx])
			spinIdx = (spinIdx + 1) % len(spinner)
			time.Sleep(watchPollInterval)
			continue
		}

		if release == nil {
			// Race: the release row is being inserted as we poll.
			// Very brief, but possible. Keep waiting.
			fmt.Printf("\r%s Registering release...", spinner[spinIdx])
			spinIdx = (spinIdx + 1) % len(spinner)
			time.Sleep(watchPollInterval)
			continue
		}

		statusChanged := release.Status != lastReleaseStatus

		// Stream build output as soon as the build is in flight. We
		// only print the suffix beyond what we've already shown so the
		// loop is cheap to run every tick.
		if streamBuildLogs && release.Status == "building" {
			printedBuildLogBytes, buildLogStreamStarted = tailBuildLogs(
				client, appID, releaseID, printedBuildLogBytes, buildLogStreamStarted)
		}

		// Only redraw the status line when something actually changed.
		// While we're tailing build logs we suppress the spinner —
		// otherwise it overprints log lines and looks like a glitch.
		if statusChanged {
			if !(streamBuildLogs && buildLogStreamStarted) {
				fmt.Printf("\r\033[K")
			}
			printReleaseStatus(release)
			lastReleaseStatus = release.Status
		} else if !buildLogStreamStarted {
			fmt.Printf("\r%s %s", spinner[spinIdx], releaseStatusMessage(release.Status))
			spinIdx = (spinIdx + 1) % len(spinner)
		}

		switch release.Status {
		case "build_failed":
			// Flush any tail we haven't shown yet so the user sees the
			// actual error line, not a generic "build failed" header.
			if streamBuildLogs {
				printedBuildLogBytes, _ = tailBuildLogs(
					client, appID, releaseID, printedBuildLogBytes, buildLogStreamStarted)
			}
			fmt.Printf("\r\033[K")
			color.New(color.FgRed, color.Bold).Println("✗ Build failed!")
			fmt.Println("  Run `deployado releases logs " + releaseID + "` to see the full build output.")
			os.Exit(1)
		case "ready":
			// Drain anything we haven't tailed yet (catches the last
			// few lines that arrived between the previous poll and the
			// status flip).
			if streamBuildLogs {
				printedBuildLogBytes, _ = tailBuildLogs(
					client, appID, releaseID, printedBuildLogBytes, buildLogStreamStarted)
			}
			fmt.Printf("\r\033[K")
			color.New(color.FgGreen).Println("✓ Image built and pushed")
			// Fall through to phase 2 — deployment polling.
			goto deploymentPhase
		}

		time.Sleep(watchPollInterval)
	}
	} // end phase-1 block — locals don't leak into deploymentPhase

deploymentPhase:
	// Phase 2 — wait for the deployment to roll out.
	//
	// When streamRuntimeLogs is on, we also tail the container's
	// stdout/stderr starting from the deployment's StartedAt. This
	// is critical for catching crash loops: the deployment-status
	// row only flips to "failed" after several retry cycles, but
	// the actual error (uncaught exception, missing env var, etc.)
	// is in the very first second of runtime output.
	lastDeploymentStatus := ""
	// Initialized to "" → on the first tick we capture the deployment's
	// StartedAt and use that as the watermark. Avoids dumping minutes
	// of pre-deploy log history.
	runtimeLogWatermark := ""
	runtimeStreamStarted := false

	for {
		deployments, err := client.ListDeployments(appID, 1, releaseID)
		if err != nil {
			return fmt.Errorf("failed to get deployment status: %w", err)
		}

		if len(deployments) == 0 {
			fmt.Printf("\r%s Waiting for deployment to start...", spinner[spinIdx])
			spinIdx = (spinIdx + 1) % len(spinner)
			time.Sleep(watchPollInterval)
			continue
		}

		deployment := deployments[0]

		// Establish the runtime-log watermark from the deployment's
		// StartedAt the first time we see it. After that, we only
		// advance the watermark inside tailRuntimeLogs as we print
		// new entries.
		if streamRuntimeLogs && runtimeLogWatermark == "" && deployment.StartedAt != "" {
			runtimeLogWatermark = deployment.StartedAt
		}

		// Tail runtime output as soon as the container is up enough
		// to produce logs. "running" / "preparing" are the live
		// states; we skip "pending" because the container doesn't
		// exist yet and the GetLogs endpoint will 404.
		if streamRuntimeLogs && shouldStreamRuntime(deployment.Status) {
			runtimeLogWatermark, runtimeStreamStarted = tailRuntimeLogs(
				client, appID, runtimeLogWatermark, runtimeStreamStarted)
		}

		statusChanged := deployment.Status != lastDeploymentStatus

		if statusChanged {
			if !(streamRuntimeLogs && runtimeStreamStarted) {
				fmt.Printf("\r\033[K")
			}
			printDeploymentStatus(deployment)
			lastDeploymentStatus = deployment.Status
		} else if !runtimeStreamStarted {
			fmt.Printf("\r%s %s", spinner[spinIdx], statusMessage(deployment.Status))
			spinIdx = (spinIdx + 1) % len(spinner)
		}

		switch deployment.Status {
		case "healthy":
			// Drain any last runtime lines that arrived between the
			// previous poll and the status flip — same pattern as
			// the build-log drain on phase-1 success.
			if streamRuntimeLogs {
				runtimeLogWatermark, _ = tailRuntimeLogs(
					client, appID, runtimeLogWatermark, runtimeStreamStarted)
			}
			fmt.Printf("\r\033[K")
			color.New(color.FgGreen, color.Bold).Println("✓ Deployment successful!")
			if deployment.Version != "" {
				fmt.Printf("  Version: %s\n", deployment.Version)
			}
			return nil

		case "failed":
			// Especially important here: the user wants to see the
			// crash output, not just "failed" with a generic reason.
			if streamRuntimeLogs {
				runtimeLogWatermark, _ = tailRuntimeLogs(
					client, appID, runtimeLogWatermark, runtimeStreamStarted)
			}
			fmt.Printf("\r\033[K")
			color.New(color.FgRed, color.Bold).Println("✗ Deployment failed!")
			if deployment.Error != nil && *deployment.Error != "" {
				fmt.Printf("  Error: %s\n", *deployment.Error)
			}
			if deployment.FailureReason != nil && *deployment.FailureReason != "" {
				fmt.Printf("  Reason: %s\n", *deployment.FailureReason)
			}
			fmt.Println("  Inspect more with `deployado logs " + appID + "`.")
			os.Exit(1)
		}

		time.Sleep(watchPollInterval)
	}
}

// shouldStreamRuntime returns whether deployment status warrants
// fetching runtime logs. We skip "pending" (the container hasn't
// been created — the agent will 404 the logs request) and trivially
// terminal states. Anything else we treat as "container exists,
// give us the output".
func shouldStreamRuntime(status string) bool {
	switch status {
	case "pending", "":
		return false
	default:
		return true
	}
}

// tailRuntimeLogs fetches recent runtime logs and prints entries with
// a timestamp strictly newer than the watermark. Returns the updated
// watermark (last timestamp printed) and whether we've started
// streaming (used by the caller to suppress the spinner).
//
// Errors are swallowed; this runs on a polling tick and a transient
// failure shouldn't kill the watcher loop.
func tailRuntimeLogs(
	client *api.Client,
	appID, watermark string,
	streamStarted bool,
) (string, bool) {
	logs, err := client.GetLogs(appID, 100)
	if err != nil || logs == nil {
		return watermark, streamStarted
	}

	fresh, newWatermark := filterFreshLogs(logs.Logs, watermark)
	if len(fresh) == 0 {
		return watermark, streamStarted
	}

	if !streamStarted {
		fmt.Printf("\r\033[K")
		color.New(color.FgCyan).Println("─── runtime output ───")
		streamStarted = true
	}

	for _, entry := range fresh {
		// Color stderr lines red so a panic stack is visible at a glance
		// against the wall of stdout.
		if entry.Stream == "stderr" {
			color.New(color.FgRed).Printf("%s %s\n", entry.Timestamp, entry.Message)
		} else {
			fmt.Printf("%s %s\n", entry.Timestamp, entry.Message)
		}
	}

	return newWatermark, streamStarted
}

// filterFreshLogs returns the subset of entries with a timestamp
// strictly greater than the watermark, plus the new watermark (max
// timestamp seen). RFC3339 timestamps from the same source compare
// correctly lexicographically — that's what we lean on here.
//
// Defensive on the empty-watermark case: with no watermark, EVERY
// entry is fresh. That's the "first tick of a brand new deployment"
// path, and a slightly noisy first burst is preferable to silently
// dropping the very first runtime output.
func filterFreshLogs(entries []api.LogEntry, watermark string) ([]api.LogEntry, string) {
	if len(entries) == 0 {
		return nil, watermark
	}

	var fresh []api.LogEntry
	newWatermark := watermark
	for _, e := range entries {
		if e.Timestamp > watermark {
			fresh = append(fresh, e)
			if e.Timestamp > newWatermark {
				newWatermark = e.Timestamp
			}
		}
	}
	return fresh, newWatermark
}

// nextBuildLogChunk is the pure part of build-log tailing: given the
// current full buffer from the API and the byte cursor of what we've
// already printed, return the new cursor and the unprinted suffix.
//
// We treat the buffer as an append-only string (the control plane
// only ever grows it). Defensive on truncation: if for any reason the
// new buffer is *shorter* than the cursor (e.g. a different release
// payload), we reset to the full new buffer rather than slicing into
// negative space and panicking.
func nextBuildLogChunk(currentBuffer string, cursor int) (int, string) {
	total := len(currentBuffer)
	// Truncation defense first — a cursor that's somehow out of range
	// must reset to the new buffer, NOT silently report "nothing new".
	// (Order matters: the equality case `cursor == total` falls
	// through here as a legitimate "all caught up" no-op.)
	if cursor < 0 || cursor > total {
		return total, currentBuffer
	}
	if total == cursor {
		return cursor, ""
	}
	return total, currentBuffer[cursor:]
}

// tailBuildLogs fetches the latest build-log buffer for a release and
// prints whatever's beyond the cursor the caller passed in. Returns
// the new cursor position (so the caller can persist it for the next
// poll) and a "we've printed at least one line" flag (used by the
// caller to suppress the spinner once log lines are flowing).
//
// Errors are swallowed on purpose — a transient 5xx on this endpoint
// should not crash the watcher loop. The next tick will retry.
func tailBuildLogs(
	client *api.Client,
	appID, releaseID string,
	cursor int,
	streamStarted bool,
) (int, bool) {
	logs, err := client.GetBuildLogs(appID, releaseID)
	if err != nil || logs == nil {
		return cursor, streamStarted
	}

	newCursor, suffix := nextBuildLogChunk(logs.Logs, cursor)
	if suffix == "" {
		return newCursor, streamStarted
	}

	if !streamStarted {
		// First time we have output — print a one-line header so the
		// user knows the wall of text below is build output, not
		// runtime logs. Subsequent ticks just append.
		fmt.Printf("\r\033[K")
		color.New(color.FgCyan).Println("─── build output ───")
		streamStarted = true
	}

	// Print only the newly-appended bytes. Build output is line-
	// buffered upstream, but the last chunk may not end on a newline
	// — that's fine, the next tick picks it up.
	fmt.Print(suffix)

	return newCursor, streamStarted
}

// findRelease looks up a single release by ID via the list endpoint.
// We pull the most recent 20 releases since the one we just triggered
// is almost certainly near the top. Returns nil if not found yet
// (the row is being inserted — happens in the first ~100ms of a deploy).
func findRelease(client *api.Client, appID, releaseID string) (*api.Release, error) {
	releases, err := client.ListReleases(appID, 20)
	if err != nil {
		return nil, err
	}
	for _, r := range releases {
		if r.ID == releaseID {
			return &r, nil
		}
	}
	return nil, nil
}

func printReleaseStatus(r *api.Release) {
	var statusColor *color.Color
	switch r.Status {
	case "pending":
		statusColor = color.New(color.FgYellow)
	case "building":
		statusColor = color.New(color.FgCyan)
	case "ready":
		statusColor = color.New(color.FgGreen)
	case "build_failed":
		statusColor = color.New(color.FgRed)
	default:
		statusColor = color.New(color.FgWhite)
	}
	statusColor.Printf("● %s\n", releaseStatusMessage(r.Status))
}

func releaseStatusMessage(status string) string {
	switch status {
	case "pending":
		return "Build queued..."
	case "building":
		return "Building image..."
	case "ready":
		return "Image ready"
	case "build_failed":
		return "Build failed"
	default:
		return "Release: " + status
	}
}

func printDeploymentStatus(d api.Deployment) {
	var statusColor *color.Color
	switch d.Status {
	case "pending":
		statusColor = color.New(color.FgYellow)
	case "preparing", "running":
		statusColor = color.New(color.FgCyan)
	case "healthy":
		statusColor = color.New(color.FgGreen)
	case "failed":
		statusColor = color.New(color.FgRed)
	default:
		statusColor = color.New(color.FgWhite)
	}

	statusColor.Printf("● %s", statusMessage(d.Status))
	if d.Version != "" {
		fmt.Printf(" (v%s)", d.Version)
	}
	fmt.Println()
}

func statusMessage(status string) string {
	switch status {
	case "pending":
		return "Pending..."
	case "preparing":
		return "Preparing deployment..."
	case "running":
		return "Running deployment..."
	case "healthy":
		return "Deployment successful"
	case "failed":
		return "Deployment failed"
	default:
		return status
	}
}

// detectAppFromDirectory attempts to find a matching app based on the
// git remote URL of the current directory
func detectAppFromDirectory(client *api.Client) (*api.App, error) {
	// Check if we're in a git repository
	if !git.IsGitRepository() {
		return nil, fmt.Errorf("not in a git repository. Please specify an app-id or run from a git repository")
	}

	// Get the remote URL
	remoteURL, err := git.GetRemoteURL()
	if err != nil {
		return nil, fmt.Errorf("failed to get git remote URL: %w", err)
	}

	normalizedRemote := git.NormalizeRepoURL(remoteURL)

	// List all apps and find one with matching repository
	apps, err := client.ListApps()
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}

	var matchingApps []*api.App
	for i := range apps {
		app := &apps[i]
		if git.NormalizeRepoURL(app.RepositoryURL) == normalizedRemote {
			matchingApps = append(matchingApps, app)
		}
	}

	if len(matchingApps) == 0 {
		return nil, fmt.Errorf("no app found matching repository %s\n\nCreate one with:\n  deployado launch", remoteURL)
	}

	if len(matchingApps) == 1 {
		return matchingApps[0], nil
	}

	// Multiple apps match - try to match by branch
	currentBranch, _ := git.GetCurrentBranch()
	if currentBranch != "" {
		for _, app := range matchingApps {
			if app.Branch == currentBranch {
				return app, nil
			}
		}
	}

	// Still multiple matches - show them and ask user to specify
	var appNames []string
	for _, app := range matchingApps {
		appNames = append(appNames, fmt.Sprintf("%s (%s)", app.Name, app.Slug))
	}

	return nil, fmt.Errorf("multiple apps found for this repository: %v\n\nPlease specify one:\n  deployado deploy <app-slug>", appNames)
}
