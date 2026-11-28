package server

import "runtime/debug"

// BuildCommit is the full source commit, injected from cmd/artex (which gets it
// from -ldflags "-X main.revision=<sha>"). Empty when the build had no git
// context; currentBuildInfo falls back to the VCS stamp Go embeds for local
// builds.
var BuildCommit = ""

// buildInfo is the build identity reported by GET /api/health. Version is the
// value shown in the UI top bar; Commit lets "dev" builds still name their
// source revision.
type buildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	GoVersion string `json:"go_version,omitempty"`
	BuildTime string `json:"build_time,omitempty"`
	Modified  bool   `json:"modified,omitempty"`
}

func currentBuildInfo(version, commit string) buildInfo {
	bi := buildInfo{Version: version, Commit: commit}
	if info, ok := debug.ReadBuildInfo(); ok {
		bi.GoVersion = info.GoVersion
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if bi.Commit == "" {
					bi.Commit = s.Value
				}
			case "vcs.time":
				bi.BuildTime = s.Value
			case "vcs.modified":
				bi.Modified = s.Value == "true"
			}
		}
	}
	return bi
}
