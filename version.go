package terminal

import "runtime/debug"

// BuildVersion resolves the identity string a running binary reports as its build version —
// the value behind `GET /version`, the `--version` output, and the badge in the top-right of
// the UI.
//
// # Why this lives in the shared package
//
// TWO binaries surface a build version: the standalone `dw-terminal` and deepwork-pro's
// `dw-host` (which embeds this package and, until now, never set Config.Version at all — so
// pro's badge had no source and simply never rendered). "What does a build's identity look
// like" is one rule, so it gets one implementation here rather than one per binary.
//
// # The rule
//
//  1. A stamped build wins. Releases inject a clean tag via
//     `-ldflags "-X main.version=v0.7.14"` (goreleaser) and build.sh injects a `git describe`
//     (e.g. "v0.7.14-3-gb2535a0"). Either is already the truth; pass it through untouched.
//  2. An UNSTAMPED build (`go build` / `go install`, and pro's run_cli.sh, none of which pass
//     -ldflags) would otherwise report the bare sentinel "dev" — which cannot answer the ONE
//     question a version badge exists to answer: *is this the code I just built?* Go stamps
//     every module build with VCS metadata automatically (debug.ReadBuildInfo), so we enrich
//     it into "dev-<shorthash>" (+"-dirty" when the tree had uncommitted changes) for free,
//     with no build-script changes required in either repo.
//
// The string returned here is the FULL, honest identity — deliberately not the abbreviated
// form the UI badge shows. Shortening is a presentation decision and belongs to the frontend
// (see frontend/src/composables/cli/buildVersion.ts), which keeps the full string in the
// badge's tooltip. A server that shortened its own answer would be throwing away the detail
// `--version` and bug reports need.
//
// `injected` is the link-time variable of the calling binary ("dev" or "" when unstamped).
func BuildVersion(injected string) string {
	if injected != "" && injected != "dev" {
		return injected
	}
	fallback := injected
	if fallback == "" {
		fallback = "dev"
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return fallback
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return fallback
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	v := "dev-" + rev
	if dirty {
		v += "-dirty"
	}
	return v
}
