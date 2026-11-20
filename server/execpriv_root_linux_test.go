//go:build linux

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	actool "github.com/Autumn-27/norma/tool"
)

// Root tests of the three executor paths: the Bash tool through the PATH shim, a
// custom tool through SysProcAttr.Credential, and an MCP stdio command through the
// drop shim. Each runs the same probe as the agent would and must show the executor
// identity, no access to a 0600 root file or to the server's /proc environ, and a
// writable HOME and cache.

type execRootFixture struct {
	priv                     ExecPrivilege
	data, home, secret, work string
}

func newExecRootFixture(t *testing.T) *execRootFixture {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	old := syscall.Umask(0o022)
	syscall.Umask(old)
	t.Cleanup(func() { syscall.Umask(old) })
	// InstallBashShim and PrepareExecPrivilege set these process-wide; restore them.
	for _, k := range []string{"PATH", envExecBash, envExecUID, envExecGID, envExecHome} {
		t.Setenv(k, os.Getenv(k))
	}
	// A data directory like /app/data: root-owned and traversable.
	dataDir, err := os.MkdirTemp("", "artex-exec-root-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dataDir) })
	if err := os.Chmod(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	priv, err := ResolveExecPrivilege(DefaultExecUID, DefaultExecGID, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agent.SetRunDirMaker(nil) }) // PrepareExecPrivilege sets it process-wide
	if err := PrepareExecPrivilege(&priv, dataDir, "", ""); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dataDir, "root-only.key")
	if err := os.WriteFile(secret, []byte("root-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(secret); err != nil {
		t.Fatalf("root must read its own key: %v", err)
	}
	// The run directory of task 1's main agent, created as the agent creates it.
	work := agent.EnsureRunDir(dataDir, 1, 0)
	return &execRootFixture{priv: priv, data: dataDir, home: filepath.Join(dataDir, execHomeDir), secret: secret, work: work}
}

// probe is the shell script each path runs as the executor.
func (f *execRootFixture) probe() string {
	return fmt.Sprintf(`echo "uid=$(id -u) gid=$(id -g) groups=$(id -G)"
if cat '%s' >/dev/null 2>&1; then echo secret=read; else echo secret=denied; fi
if cat /proc/%d/environ >/dev/null 2>&1; then echo environ=read; else echo environ=denied; fi
echo "home=$HOME cache=$XDG_CACHE_HOME"
if mkdir -p "$XDG_CACHE_HOME/probe" && echo ok > "$XDG_CACHE_HOME/probe/x"; then echo cache=written; else echo cache=failed; fi
`, f.secret, os.Getpid())
}

func (f *execRootFixture) check(t *testing.T, path, out string) {
	t.Helper()
	for _, want := range []string{
		fmt.Sprintf("uid=%d gid=%d groups=%d", DefaultExecUID, DefaultExecGID, DefaultExecGID),
		"secret=denied",
		"environ=denied",
		"home=" + f.home + " cache=" + filepath.Join(f.home, ".cache"),
		"cache=written",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%s: want %q in the probe output:\n%s", path, want, out)
		}
	}
	fi, err := os.Stat(filepath.Join(f.home, ".cache", "probe", "x"))
	if err != nil {
		t.Fatalf("%s: cache file missing: %v", path, err)
	}
	if st := fi.Sys().(*syscall.Stat_t); st.Uid != DefaultExecUID {
		t.Errorf("%s: cache file owned by uid %d, want %d", path, st.Uid, DefaultExecUID)
	}
	t.Logf("%s probe output:\n%s", path, out)
}

// Norma's Bash tool runs exec.Command("bash"): the PATH shim drops and execs bash.
func TestExecutorBashShimAsRoot(t *testing.T) {
	f := newExecRootFixture(t)
	if err := InstallBashShim(f.priv); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.SplitList(os.Getenv("PATH"))[0]) })
	in, _ := json.Marshal(map[string]string{"command": f.probe()})
	res, err := actool.NewBash().Call(context.Background(), in, &actool.ToolContext{WorkingDir: f.work})
	if err != nil {
		t.Fatal(err)
	}
	f.check(t, "bash shim", res.Flatten())
}

// A custom script tool runs through execPython, which applies the credential.
func TestExecutorCustomToolAsRoot(t *testing.T) {
	f := newExecRootFixture(t)
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is needed for the custom-tool path: %v", err)
	}
	code := `import os, subprocess
print(subprocess.run(["sh", "-c", os.environ["PROBE"]], capture_output=True, text=True).stdout)`
	out, err := execPython(context.Background(), py, "probe", code, nil, f.work, []string{"PROBE=" + f.probe()}, 30*time.Second, f.priv)
	if err != nil {
		t.Fatal(err)
	}
	f.check(t, "custom tool", out)
}

// The script of a custom tool is written to the root-owned script directory, not
// under the run directory: the agent turned <run dir>/.tools into a symlink to a
// root-only directory, and nothing may be written through it. The script runs as
// the executor, which can read it and cannot change the directory it is in.
func TestExecPythonScriptDirAsRoot(t *testing.T) {
	f := newExecRootFixture(t)
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is needed for the custom-tool path: %v", err)
	}
	dataDir := filepath.Dir(f.secret)
	dir := f.priv.ScriptDir
	if dir != filepath.Join(dataDir, execScriptDir) {
		t.Fatalf("script directory = %q, want %s under the data directory", dir, execScriptDir)
	}
	checkDir := func() {
		t.Helper()
		fi, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		st := fi.Sys().(*syscall.Stat_t)
		if !fi.IsDir() || st.Uid != 0 || st.Gid != DefaultExecGID || fi.Mode().Perm() != 0o750 || fi.Mode()&os.ModeSetgid != 0 {
			t.Fatalf("script directory: mode %v uid %d gid %d, want a directory 0750 root:%d", fi.Mode(), st.Uid, st.Gid, DefaultExecGID)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Fatalf("script directory holds %v (%v), want it empty", entries, err)
		}
	}
	checkDir()

	trap := filepath.Join(dataDir, "trap")
	if err := os.Mkdir(trap, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(trap, filepath.Join(f.work, ".tools")); err != nil {
		t.Fatal(err)
	}

	code := `import errno, os
d = os.path.dirname(os.path.abspath(__file__))
print("uid=%d gid=%d" % (os.getuid(), os.getgid()))
print("script_dir=" + d)
st = os.stat(__file__)
print("script=%o uid=%d gid=%d" % (st.st_mode & 0o777, st.st_uid, st.st_gid))
def attempt(name, fn):
    try:
        fn()
        print(name + "=done")
    except OSError as e:
        print(name + "=" + errno.errorcode.get(e.errno, str(e.errno)))
attempt("create", lambda: open(os.path.join(d, "planted.py"), "w").close())
attempt("symlink", lambda: os.symlink("/etc/passwd", os.path.join(d, "planted-link")))
attempt("mkdir", lambda: os.mkdir(os.path.join(d, "planted-dir")))
attempt("rename", lambda: os.rename(__file__, __file__ + ".moved"))
attempt("unlink", lambda: os.unlink(__file__))
attempt("write", lambda: open(__file__, "a").close())
attempt("chmod", lambda: os.chmod(d, 0o777))
`
	out, err := execPython(context.Background(), py, "probe", code, nil, f.work, nil, 30*time.Second, f.priv)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("script probe output:\n%s", out)
	for _, want := range []string{
		fmt.Sprintf("uid=%d gid=%d\n", DefaultExecUID, DefaultExecGID),
		"script_dir=" + dir + "\n",
		fmt.Sprintf("script=640 uid=0 gid=%d\n", DefaultExecGID),
		"create=EACCES\n", "symlink=EACCES\n", "mkdir=EACCES\n", "rename=EACCES\n", "unlink=EACCES\n", "write=EACCES\n",
		"chmod=EPERM\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the probe output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "[exit:") || strings.Contains(out, "=done") {
		t.Errorf("the script failed or changed its directory:\n%s", out)
	}
	// The script is removed, and nothing went through the planted symlink or to the
	// run directory.
	checkDir()
	if entries, err := os.ReadDir(trap); err != nil || len(entries) != 0 {
		t.Fatalf("the symlinked .tools target holds %v (%v), want it empty", entries, err)
	}
	if fi, err := os.Lstat(filepath.Join(f.work, ".tools")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(".tools in the run directory changed: %v %v", fi, err)
	}
}

// An MCP stdio command is started as `<server> __artex-exec-drop uid gid -- cmd`;
// mcp.NewStdioClient runs it with exec.Command and the server environment.
func TestExecutorMCPStdioAsRoot(t *testing.T) {
	f := newExecRootFixture(t)
	command, args, err := execDropCommand(f.priv, "sh", []string{"-c", f.probe()})
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(command, args...)
	c.Dir = f.work
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	f.check(t, "mcp stdio", string(out))
}

// applyExecCredential sets the credential and the executor's HOME; the later env
// entry wins in exec.Cmd. Runs without root: nothing is started.
func TestApplyExecCredentialSetsExecutorHome(t *testing.T) {
	t.Setenv(envExecHome, "/data/exec-home")
	c := exec.Command("true")
	c.Env = []string{"HOME=/root", "KEEP=1"}
	applyExecCredential(c, ExecPrivilege{Enabled: true, UID: 10001, GID: 10002})
	if cr := c.SysProcAttr.Credential; cr == nil || cr.Uid != 10001 || cr.Gid != 10002 {
		t.Fatalf("credential = %+v", c.SysProcAttr.Credential)
	}
	want := []string{"HOME=/root", "KEEP=1", "HOME=/data/exec-home", "XDG_CACHE_HOME=/data/exec-home/.cache"}
	if strings.Join(c.Env, "|") != strings.Join(want, "|") {
		t.Fatalf("env = %v, want %v", c.Env, want)
	}
	disabled := exec.Command("true")
	applyExecCredential(disabled, ExecPrivilege{})
	if disabled.SysProcAttr != nil || disabled.Env != nil {
		t.Fatal("a disabled privilege must leave the command unchanged")
	}
}
