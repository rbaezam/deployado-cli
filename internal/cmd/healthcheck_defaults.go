package cmd

import "github.com/deployado/cli/internal/detect"

func defaultHealthCheckPath(runtime string, stack detect.Stack, servicePath string) string {
	if runtime == "static" || stack == detect.StackStatic {
		return "/"
	}

	// Backward compatibility for older blueprints that tagged frontend apps as node.
	if runtime == "node" && servicePath != "" && detect.IsStaticSite(servicePath) {
		return "/"
	}

	return "/health"
}
