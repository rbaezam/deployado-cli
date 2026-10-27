package git

import (
	"os/exec"
	"regexp"
	"strings"
)

// GetRemoteURL returns the remote URL of the current git repository
func GetRemoteURL() (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// NormalizeRepoURL converts a git URL to a normalized HTTPS format
// for comparison purposes. Handles:
// - git@github.com:user/repo.git
// - https://github.com/user/repo.git
// - https://github.com/user/repo
func NormalizeRepoURL(url string) string {
	url = strings.TrimSpace(url)

	// Remove .git suffix
	url = strings.TrimSuffix(url, ".git")

	// Convert SSH format to HTTPS
	// git@github.com:user/repo -> https://github.com/user/repo
	sshPattern := regexp.MustCompile(`^git@([^:]+):(.+)$`)
	if matches := sshPattern.FindStringSubmatch(url); matches != nil {
		url = "https://" + matches[1] + "/" + matches[2]
	}

	// Ensure https:// prefix
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		url = "https://" + url
	}

	// Normalize to lowercase for comparison
	url = strings.ToLower(url)

	return url
}

// GetCurrentBranch returns the current git branch name
func GetCurrentBranch() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// IsGitRepository checks if the current directory is a git repository
func IsGitRepository() bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	err := cmd.Run()
	return err == nil
}
