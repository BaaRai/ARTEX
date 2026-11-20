//go:build linux

package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	pgdb "github.com/Autumn-27/artex/db"
)

// Root tests of the run-directory layout: no agent working directory is an ancestor
// of another, the parents of the run directories are root:root 0755, and only the
// leaves are shared with the executor group.

// checkDir asserts that path is a directory owned by root with the group gid and
// exactly mode (permission bits plus setuid, setgid and sticky).
func checkDir(t *testing.T, path string, gid uint32, mode os.FileMode) {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Errorf("%s: %v", path, err)
		return
	}
	st := fi.Sys().(*syscall.Stat_t)
	got := fi.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	if !fi.IsDir() || st.Uid != 0 || st.Gid != gid || got != mode {
		t.Errorf("%s: %v uid %d gid %d, want a directory %v root:%d", path, fi.Mode(), st.Uid, st.Gid, mode, gid)
		return
	}
	t.Logf("%s: %v uid %d gid %d", path, fi.Mode(), st.Uid, st.Gid)
}

const (
	parentDirMode = os.FileMode(0o755)
	leafDirMode   = 0o775 | os.ModeSetgid
)

// PrepareExecPrivilege and ensureRunDir, for a task with a main agent and a worker
// and for a chat session.
func TestRunDirLayoutAsRoot(t *testing.T) {
	f := newExecRootFixture(t)
	task := filepath.Join(f.data, "tasks", "1")
	mainDir := f.work
	worker := agent.EnsureRunDir(f.data, 1, 7)
	session := agent.EnsureSessionDir(f.data, "conv-3")
	if mainDir != filepath.Join(task, "main") || worker != filepath.Join(task, "i7") || session != filepath.Join(f.data, "sessions", "conv-3") {
		t.Fatalf("run directories: main %s, worker %s, session %s", mainDir, worker, session)
	}
	check := func() {
		t.Helper()
		for _, dir := range []string{filepath.Join(f.data, "tasks"), filepath.Join(f.data, "sessions"), task} {
			checkDir(t, dir, 0, parentDirMode)
		}
		for _, dir := range []string{mainDir, worker, session} {
			checkDir(t, dir, DefaultExecGID, leafDirMode)
		}
	}
	check()
	// A later run of the same agents finds the directories and leaves them so.
	agent.EnsureRunDir(f.data, 1, 0)
	agent.EnsureRunDir(f.data, 1, 7)
	agent.EnsureSessionDir(f.data, "conv-3")
	check()
	// What the server creates in a leaf keeps the executor group and is group-writable.
	uploads := filepath.Join(mainDir, "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(uploads, "report.txt")
	if err := os.WriteFile(file, []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	checkDir(t, uploads, DefaultExecGID, 0o755|os.ModeSetgid)
	fi, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if st := fi.Sys().(*syscall.Stat_t); st.Gid != DefaultExecGID || fi.Mode().Perm() != 0o664 {
		t.Errorf("%s: %v gid %d, want 0664 with gid %d", file, fi.Mode(), st.Gid, DefaultExecGID)
	}
}

// An executor process started in the main agent's run directory cannot rename or
// remove a worker's run directory or its own, and cannot create an entry in the
// task directory or in tasks/.
func TestRunDirSiblingsProtectedAsRoot(t *testing.T) {
	f := newExecRootFixture(t)
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is needed for the executor probe: %v", err)
	}
	tasks := filepath.Join(f.data, "tasks")
	task := filepath.Join(tasks, "1")
	worker := agent.EnsureRunDir(f.data, 1, 7)

	code := `import errno, os
main = os.getcwd()
task = os.path.dirname(main)
tasks = os.path.dirname(task)
worker = os.path.join(task, "i7")
print("uid=%d gid=%d" % (os.getuid(), os.getgid()))
print("cwd=" + main)
def attempt(name, fn):
    try:
        fn()
        print(name + "=done")
    except OSError as e:
        print(name + "=" + errno.errorcode.get(e.errno, str(e.errno)))
attempt("own_write", lambda: open("own.txt", "w").close())
attempt("rename_worker", lambda: os.rename(worker, os.path.join(task, "i7.moved")))
attempt("rename_worker_into_main", lambda: os.rename(worker, os.path.join(main, "i7")))
attempt("rmdir_worker", lambda: os.rmdir(worker))
attempt("unlink_worker", lambda: os.unlink(worker))
attempt("rename_main", lambda: os.rename(main, os.path.join(task, "main.moved")))
attempt("rmdir_main", lambda: os.rmdir(main))
attempt("create_in_task", lambda: open(os.path.join(task, "planted"), "w").close())
attempt("mkdir_in_task", lambda: os.mkdir(os.path.join(task, "i8")))
attempt("symlink_in_task", lambda: os.symlink("/etc", os.path.join(task, "i9")))
attempt("create_in_tasks", lambda: open(os.path.join(tasks, "planted"), "w").close())
attempt("mkdir_in_tasks", lambda: os.mkdir(os.path.join(tasks, "2")))
attempt("chmod_task", lambda: os.chmod(task, 0o777))
attempt("chmod_worker", lambda: os.chmod(worker, 0o777))
`
	out, err := execPython(context.Background(), py, "probe", code, nil, f.work, nil, 30*time.Second, f.priv)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("executor probe output:\n%s", out)
	for _, want := range []string{
		fmt.Sprintf("uid=%d gid=%d\n", DefaultExecUID, DefaultExecGID),
		"cwd=" + f.work + "\n",
		"own_write=done\n",
		"chmod_task=EPERM\n", "chmod_worker=EPERM\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the probe output:\n%s", want, out)
		}
	}
	for _, name := range []string{
		"rename_worker", "rename_worker_into_main", "rmdir_worker", "unlink_worker", "rename_main", "rmdir_main",
		"create_in_task", "mkdir_in_task", "symlink_in_task", "create_in_tasks", "mkdir_in_tasks",
	} {
		if !strings.Contains(out, name+"=EACCES\n") && !strings.Contains(out, name+"=EPERM\n") {
			t.Errorf("%s was not refused with EACCES or EPERM:\n%s", name, out)
		}
	}
	if strings.Contains(out, "[exit:") {
		t.Errorf("the probe failed:\n%s", out)
	}
	// Nothing moved and nothing was planted.
	for dir, want := range map[string]string{tasks: "1", task: "i7 main"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		if got := strings.Join(names, " "); got != want {
			t.Errorf("%s holds %q, want %q", dir, got, want)
		}
	}
	checkDir(t, task, 0, parentDirMode)
	checkDir(t, worker, DefaultExecGID, leafDirMode)
	checkDir(t, f.work, DefaultExecGID, leafDirMode)
}

// A data directory written before this layout: the task directory was the main
// agent's working directory and was shared with the executor group. The start-up
// pass takes the task directory back, shares its run directories and leaves the
// files of the task directory where they are. A symlink planted where a run
// directory goes is refused and its target is not changed.
func TestRunDirLayoutOnExistingTreeAsRoot(t *testing.T) {
	f := newExecRootFixture(t)
	gid := int(DefaultExecGID)
	task := filepath.Join(f.data, "tasks", "2")
	oldWorker := filepath.Join(task, "i4")
	nested := filepath.Join(oldWorker, "recon")
	for _, dir := range []string{task, oldWorker, nested} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// The old state of the task directory: group-shared and setgid.
	if err := os.Chown(task, 0, gid); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(task, 0o775|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(task, "notes.txt")
	workerFile := filepath.Join(nested, "hosts.txt")
	for _, file := range []string{oldFile, workerFile} {
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(file, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	trap := filepath.Join(f.data, "trap")
	if err := os.Mkdir(trap, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(trap, filepath.Join(task, "main")); err != nil {
		t.Fatal(err)
	}
	stat := func(path string) (os.FileMode, uint32) {
		t.Helper()
		fi, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode(), fi.Sys().(*syscall.Stat_t).Gid
	}
	oldMode, oldGid := stat(oldFile)

	if err := PrepareExecPrivilege(&f.priv, f.data, "", ""); err != nil {
		t.Fatal(err)
	}
	checkDir(t, task, 0, parentDirMode)
	checkDir(t, oldWorker, DefaultExecGID, leafDirMode)
	checkDir(t, nested, DefaultExecGID, 0o770|os.ModeSetgid)
	if mode, g := stat(workerFile); mode.Perm() != 0o660 || g != DefaultExecGID {
		t.Errorf("%s: %v gid %d, want 0660 with gid %d", workerFile, mode, g, DefaultExecGID)
	}
	// The file of the task directory is not moved and keeps its mode and group.
	if mode, g := stat(oldFile); mode != oldMode || g != oldGid {
		t.Errorf("%s: %v gid %d, want it left %v gid %d", oldFile, mode, g, oldMode, oldGid)
	}
	// The planted link is still a link; creating the main run directory through it
	// fails, and the directory it points to keeps its owner and mode.
	agent.EnsureRunDir(f.data, 2, 0)
	if mode, _ := stat(filepath.Join(task, "main")); mode&os.ModeSymlink == 0 {
		t.Errorf("tasks/2/main is %v, want the planted symlink left in place", mode)
	}
	if err := makeRunDir(f.data, filepath.Join(task, "main"), gid); err == nil {
		t.Error("makeRunDir accepted a run directory that is a symlink")
	}
	checkDir(t, trap, 0, 0o700)
	if entries, err := os.ReadDir(trap); err != nil || len(entries) != 0 {
		t.Errorf("the symlink target holds %v (%v), want it empty", entries, err)
	}
}

// A task archive round trip with the privilege drop on: the whole task directory is
// packed, and the restore puts the layout back although the package keeps neither
// owners nor directory modes.
func TestTaskArchiveRestoreRunDirOwnershipAsRoot(t *testing.T) {
	f := newExecRootFixture(t)
	const taskID = "1"
	task := filepath.Join(f.data, "tasks", taskID)
	worker := agent.EnsureRunDir(f.data, 1, 7)
	files := map[string]string{
		filepath.Join(f.work, "uploads", "evidence.txt"): "task evidence",
		filepath.Join(worker, "notes.txt"):               "worker notes",
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A spill file of the task, as Norma writes it.
	spillDir := agent.TaskToolOutputDir(f.data, 1, f.work)
	spillFile := filepath.Join(spillDir, "output-1-1.txt")
	if err := os.WriteFile(spillFile, []byte("spilled output"), 0o600); err != nil {
		t.Fatal(err)
	}

	stage, err := stageTaskArchiveFiles(f.data, 1, taskID, 73)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(task); !os.IsNotExist(err) {
		t.Fatalf("task directory still hot after staging: %v", err)
	}
	if _, err := os.Lstat(spillDir); !os.IsNotExist(err) {
		t.Fatalf("spill directory still hot after staging: %v", err)
	}
	archivePath := taskArchivePath(f.data, 1, taskID)
	snapshot := &pgdb.TaskArchiveSnapshot{FormatVersion: pgdb.TaskArchiveFormatVersion, TaskID: 1, ExplorationID: 73}
	_, _, checksum, err := writeTaskArchivePackage(archivePath, stage.payload, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.commit(); err != nil {
		t.Fatal(err)
	}
	extracted := filepath.Join(f.data, "restore")
	if err := extractTaskArchivePackage(archivePath, checksum, extracted); err != nil {
		t.Fatal(err)
	}
	installed, err := installTaskArchiveFiles(f.data, extracted, taskID, 1, f.priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := installed.commit(); err != nil {
		t.Fatal(err)
	}

	checkDir(t, filepath.Join(f.data, "tasks"), 0, parentDirMode)
	checkDir(t, task, 0, parentDirMode)
	checkDir(t, f.work, DefaultExecGID, leafDirMode)
	checkDir(t, worker, DefaultExecGID, leafDirMode)
	checkDir(t, filepath.Join(f.work, "uploads"), DefaultExecGID, 0o770|os.ModeSetgid)
	for path, want := range files {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("restored %s = %q, %v; want %q", path, got, err, want)
		}
		fi, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st := fi.Sys().(*syscall.Stat_t); st.Gid != DefaultExecGID || fi.Mode().Perm()&0o060 != 0o060 {
			t.Errorf("%s: %v gid %d, want group read/write with gid %d", path, fi.Mode(), st.Gid, DefaultExecGID)
		}
	}
	// The spill directory is back for the executor to read, and not to write.
	checkDir(t, filepath.Join(f.data, "spill", "tasks"), 0, parentDirMode)
	checkDir(t, spillDir, DefaultExecGID, 0o750)
	if fi, err := os.Lstat(spillFile); err != nil {
		t.Fatal(err)
	} else if st := fi.Sys().(*syscall.Stat_t); fi.Mode() != 0o640 || st.Uid != 0 || st.Gid != DefaultExecGID {
		t.Errorf("%s: %v uid %d gid %d, want 0640 root:%d", spillFile, fi.Mode(), st.Uid, st.Gid, DefaultExecGID)
	}
	// The executor works in the restored run directory again, and reads the spill.
	c := exec.Command("sh", "-c", "echo more >> uploads/evidence.txt && echo new > after-restore.txt && test \"$(cat '"+spillFile+"')\" = 'spilled output' && ! touch '"+spillDir+"/planted' 2>/dev/null && id -u")
	c.Dir = f.work
	applyExecCredential(c, f.priv)
	out, err := c.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != fmt.Sprint(DefaultExecUID) {
		t.Fatalf("executor write in the restored run directory: %v\n%s", err, out)
	}
}
