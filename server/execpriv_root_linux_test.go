//go:build linux

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	// PrepareExecPrivilege sets them process-wide.
	t.Cleanup(func() { agent.SetRunDirMaker(nil); agent.SetSpillDirMaker(nil, nil) })
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

// The directory of Norma's background-task output is created when absent. A symlink
// or a file in its place is refused, and nothing is created through the symlink.
// Runs without root.
func TestPrepareNormaTaskDir(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })
	base := t.TempDir()
	dir := filepath.Join(base, "norma")
	priv := ExecPrivilege{Enabled: true, UID: DefaultExecUID, GID: DefaultExecGID}
	for _, state := range []string{"absent", "already there"} {
		if err := prepareNormaTaskDir(dir, priv); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o755 {
			t.Fatalf("%s: %v %v, want a 0755 directory", state, fi, err)
		}
	}
	outside := t.TempDir()
	link := filepath.Join(base, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := prepareNormaTaskDir(link, priv); err == nil {
		t.Fatal("a symlink to a directory was accepted")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("the symlink target holds %v (%v), want it empty", entries, err)
	}
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareNormaTaskDir(file, priv); err == nil {
		t.Fatal("a file was accepted")
	}
}

// The executor can write in a directory it owns, in one its group can write in and
// in one others can write in. No other directory counts. Runs without root.
func TestNormaTaskDirExecWrite(t *testing.T) {
	const execUID, execGID = 10001, 10002
	for _, tc := range []struct {
		name     string
		perm     os.FileMode
		uid, gid uint32
		writable bool
	}{
		{"owned by the executor uid", 0o755, execUID, 0, true},
		{"owned by the executor uid, no write bit", 0o555, execUID, 0, true},
		{"group-writable for the executor gid", 0o775, 0, execGID, true},
		{"other-writable", 0o757, 0, 0, true},
		{"root-owned 0755", 0o755, 0, 0, false},
		{"another user, not writable by the executor", 0o755, 1000, 1000, false},
		{"group-writable for another gid", 0o775, 1000, 1000, false},
		{"the executor gid without the group write bit", 0o755, 1000, execGID, false},
	} {
		why := normaTaskDirExecWrite(tc.perm, tc.uid, tc.gid, execUID, execGID)
		if (why != "") != tc.writable {
			t.Errorf("%s (mode %v, uid %d, gid %d): reason %q, want writable=%v", tc.name, tc.perm, tc.uid, tc.gid, why, tc.writable)
		}
	}
}

// prepareNormaTaskDir applies that decision to a directory that is already there:
// one the executor can write in fails the start with its path in the error, and one
// of another user that the executor cannot write in is used with a warning. Either
// way the directory keeps its owner and mode. Runs with and without root: the
// executor ids are chosen around the ids of the directory.
func TestPrepareNormaTaskDirAlreadyThere(t *testing.T) {
	for _, tc := range []struct {
		name             string
		perm             os.FileMode
		sameUID, sameGID bool // the executor id is the directory's
		refused          bool
	}{
		{"owned by the executor uid", 0o755, true, false, true},
		{"group-writable for the executor gid", 0o775, false, true, true},
		{"other-writable", 0o757, false, false, true},
		{"another user, not writable by the executor", 0o755, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "norma")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if os.Geteuid() == 0 { // not root's: without root it is the test user's
				if err := os.Lchown(dir, 20001, 20002); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Chmod(dir, tc.perm); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(dir)
			if err != nil {
				t.Fatal(err)
			}
			st := before.Sys().(*syscall.Stat_t)
			priv := ExecPrivilege{Enabled: true, UID: st.Uid + 1, GID: st.Gid + 1}
			if tc.sameUID {
				priv.UID = st.Uid
			}
			if tc.sameGID {
				priv.GID = st.Gid
			}
			var logged bytes.Buffer
			prev := log.Writer()
			log.SetOutput(&logged)
			err = prepareNormaTaskDir(dir, priv)
			log.SetOutput(prev)
			if tc.refused && (err == nil || !strings.Contains(err.Error(), dir)) {
				t.Fatalf("err = %v, want a refusal that names %s", err, dir)
			}
			if !tc.refused && err != nil {
				t.Fatalf("a directory the executor cannot write in was refused: %v", err)
			}
			if warned := strings.Contains(logged.String(), dir); warned == tc.refused {
				t.Fatalf("warning logged = %v, want %v:\n%s", warned, !tc.refused, logged.String())
			}
			after, err := os.Lstat(dir)
			if err != nil {
				t.Fatalf("the directory is gone: %v", err)
			}
			if at := after.Sys().(*syscall.Stat_t); at.Uid != st.Uid || at.Gid != st.Gid || after.Mode() != before.Mode() {
				t.Fatalf("the directory changed: uid %d gid %d mode %v, was uid %d gid %d mode %v", at.Uid, at.Gid, after.Mode(), st.Uid, st.Gid, before.Mode())
			}
		})
	}
}

// With the drop enabled, PrepareExecPrivilege creates <temp>/norma before any agent
// process runs: root:root 0755 in a world-writable sticky temp directory, where the
// executor can neither add an entry nor rename it.
func TestNormaTaskDirAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	tmp, err := os.MkdirTemp("", "artex-norma-tmp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	if err := os.Chmod(tmp, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	f := newExecRootFixture(t)
	fi, err := os.Lstat(filepath.Join(tmp, "norma"))
	if err != nil {
		t.Fatal(err)
	}
	if st := fi.Sys().(*syscall.Stat_t); !fi.IsDir() || st.Uid != 0 || st.Gid != 0 || fi.Mode().Perm() != 0o755 {
		t.Fatalf("%s/norma: mode %v uid %d gid %d, want a directory 0755 root:root", tmp, fi.Mode(), st.Uid, st.Gid)
	}
	c := exec.Command("sh", "-c", `cd "$1" || exit 1
mkdir norma/x 2>/dev/null || echo mkdir=denied
ln -s / norma/l 2>/dev/null || echo symlink=denied
mv norma moved 2>/dev/null || echo rename=denied
id -u`, "sh", tmp)
	applyExecCredential(c, f.priv)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := fmt.Sprintf("mkdir=denied\nsymlink=denied\nrename=denied\n%d\n", DefaultExecUID)
	if string(out) != want {
		t.Fatalf("executor probe in the temp directory:\n%s\nwant:\n%s", out, want)
	}
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
