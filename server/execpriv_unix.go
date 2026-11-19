//go:build unix

package server

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Environment names the Bash shim reads. They carry no secret: the executor id and
// the real bash path.
const (
	envExecBash = "ARTEX_EXEC_BASH"
	envExecUID  = "ARTEX_EXEC_UID"
	envExecGID  = "ARTEX_EXEC_GID"
)

func execPrivilegeSupported() bool { return true }

func serverIsRoot() bool { return os.Geteuid() == 0 }

// applyExecCredential runs c as the executor identity. The server's own children
// (custom tools) use it directly; the Norma children go through the shims.
func applyExecCredential(c *exec.Cmd, p ExecPrivilege) {
	if !p.Enabled {
		return
	}
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Credential = &syscall.Credential{Uid: p.UID, Gid: p.GID, Groups: []uint32{p.GID}}
}

// prepareExecScript lets the executor read a script the server wrote: the file stays
// root-owned and becomes readable by the executor group only.
func prepareExecScript(p ExecPrivilege, path string) error {
	if !p.Enabled {
		return nil
	}
	if err := os.Chown(path, -1, int(p.GID)); err != nil {
		return err
	}
	return os.Chmod(path, 0o640)
}

// PrepareExecPrivilege sets up the state the executor needs before the server opens
// its stores. The task and chat workspace trees are shared with the executor group
// (setgid, group read/write, umask 002) instead of chowned per directory: the
// workspace is created in many places, and new subdirectories inherit the group. The key files and the
// config file (which can hold the database DSN) are restricted to root. Nothing
// else under the data directory changes; directories a root-only path needs are
// left as they are.
func PrepareExecPrivilege(p ExecPrivilege, dataDir, keyDir, configPath string) error {
	if !p.Enabled {
		return nil
	}
	// tasks/ holds the run directories of the task agents, sessions/ the chat
	// workspaces: the shell of each runs in one of them.
	for _, sub := range []string{"tasks", "sessions"} {
		root := filepath.Join(dataDir, sub)
		if err := os.MkdirAll(root, 0o775); err != nil {
			return fmt.Errorf("create %s workspace root: %w", sub, err)
		}
		if err := shareWithExecGroup(root, int(p.GID)); err != nil {
			return fmt.Errorf("share %s workspace with the executor group: %w", sub, err)
		}
	}
	tasks := filepath.Join(dataDir, "tasks")
	for _, name := range []string{"jwt.key", "secret.key"} {
		if keyDir == "" {
			break
		}
		path := filepath.Join(keyDir, name)
		if _, err := os.Stat(path); err == nil {
			if err := os.Chmod(path, 0o600); err != nil {
				return fmt.Errorf("restrict %s to root: %w", path, err)
			}
		}
	}
	if configPath != "" {
		if fi, err := os.Stat(configPath); err == nil && fi.Mode().Perm()&0o077 != 0 {
			if err := os.Chmod(configPath, 0o600); err != nil {
				return fmt.Errorf("restrict %s to root: %w", configPath, err)
			}
			log.Printf("[exec] %s set to 0600: it can hold the database DSN", configPath)
		}
	}
	// Files and directories the server creates from now on are group-writable, so
	// the executor can write into the task workspace inside the shared tree.
	syscall.Umask(0o002)
	log.Printf("[exec] agent processes run as uid %d gid %d; task workspace %s is shared with that group", p.UID, p.GID, tasks)
	return nil
}

// shareWithExecGroup gives the executor group the tree under root: the group owns
// each entry and gets read/write (and traverse on directories), and directories set
// the setgid bit so anything created below them keeps the group.
func shareWithExecGroup(root string, gid int) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := os.Lchown(path, -1, gid); err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm() | 0o060
		if d.IsDir() {
			mode = (info.Mode().Perm() | 0o070) | os.ModeSetgid
		}
		return os.Chmod(path, mode)
	})
}

// InstallBashShim makes the Bash tool, the interactive shell and background tasks
// run as the executor. Norma starts them with exec.Command("bash"), which takes no
// credential, so a symlink named bash to this binary is placed first on PATH. The
// shim (RunExecShimIfRequested) drops to the executor identity and execs the real
// bash, which keeps the PID and the process group that Norma tracks.
func InstallBashShim(p ExecPrivilege) error {
	if !p.Enabled {
		return nil
	}
	realBash, err := exec.LookPath("bash")
	if err != nil {
		return fmt.Errorf("bash is needed for the agent shell: %w", err)
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the server binary: %w", err)
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return fmt.Errorf("resolve the server binary: %w", err)
	}
	dir, err := os.MkdirTemp("", "artex-exec-")
	if err != nil {
		return fmt.Errorf("create the shim directory: %w", err)
	}
	// Root-owned and traversable: the executor can run the shim, cannot change it.
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	if err := os.Symlink(self, filepath.Join(dir, "bash")); err != nil {
		return fmt.Errorf("create the bash shim: %w", err)
	}
	if err := os.Setenv(envExecBash, realBash); err != nil {
		return err
	}
	if err := os.Setenv(envExecUID, execIDString(p.UID)); err != nil {
		return err
	}
	if err := os.Setenv(envExecGID, execIDString(p.GID)); err != nil {
		return err
	}
	return os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// RunExecShimIfRequested makes the process a privilege-drop shim when it was started
// as one: as bash through the PATH shim, or as artex __artex-exec-drop for an MCP
// stdio command. It exits or execs the target; it returns only for an ordinary start.
func RunExecShimIfRequested(argv []string) {
	if len(argv) == 0 {
		return
	}
	if filepath.Base(argv[0]) == "bash" && os.Getenv(envExecBash) != "" {
		runBashShim(argv)
	}
	if len(argv) >= 2 && argv[1] == execDropArg {
		runDropShim(argv[2:])
	}
}

func runBashShim(argv []string) {
	uid, gid, err := execIDsFromEnv()
	if err != nil {
		shimFail(err)
	}
	if err := dropTo(uid, gid); err != nil {
		shimFail(err)
	}
	shimExec(os.Getenv(envExecBash), argv)
}

// runDropShim handles: <uid> <gid> -- <command> <args...>.
func runDropShim(args []string) {
	if len(args) < 3 || args[2] != "--" {
		shimFail(errors.New("drop shim needs <uid> <gid> -- <command> [args]"))
	}
	uid, errU := strconv.Atoi(args[0])
	gid, errG := strconv.Atoi(args[1])
	if errU != nil || errG != nil || len(args) < 4 {
		shimFail(errors.New("drop shim: malformed identity or command"))
	}
	command := args[3]
	target := command
	if !strings.Contains(command, "/") {
		resolved, err := exec.LookPath(command)
		if err != nil {
			shimFail(fmt.Errorf("command %q: %w", command, err))
		}
		target = resolved
	}
	if err := dropTo(uid, gid); err != nil {
		shimFail(err)
	}
	shimExec(target, args[3:])
}

func execIDsFromEnv() (int, int, error) {
	uid, errU := strconv.Atoi(os.Getenv(envExecUID))
	gid, errG := strconv.Atoi(os.Getenv(envExecGID))
	if errU != nil || errG != nil {
		return 0, 0, errors.New("executor identity missing from the environment")
	}
	return uid, gid, nil
}

// dropTo switches the calling process to uid and gid, for good: the supplementary
// groups, gid and uid all change, and the result is checked. A process already
// running as the executor (a nested bash) passes straight through.
func dropTo(uid, gid int) error {
	if os.Getuid() == uid && os.Getgid() == gid {
		return nil
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("cannot switch to uid %d: the process is not root", uid)
	}
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("setgid %d: %w", gid, err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("setuid %d: %w", uid, err)
	}
	if os.Getuid() != uid || os.Geteuid() != uid || os.Getgid() != gid {
		return fmt.Errorf("privilege drop to %d:%d did not take effect", uid, gid)
	}
	return nil
}

// shimExec replaces the process with target. It returns only on failure, and then
// the shim stops: a command must never run with the server's identity by mistake.
func shimExec(target string, argv []string) {
	err := syscall.Exec(target, argv, os.Environ())
	shimFail(fmt.Errorf("exec %s: %w", target, err))
}

func shimFail(err error) {
	fmt.Fprintf(os.Stderr, "artex: agent process not started: %v\n", err)
	os.Exit(126)
}
