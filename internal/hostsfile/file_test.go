package hostsfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsdevme/dnsmasq-manager/internal/hostsfile"
	"github.com/gsdevme/dnsmasq-manager/internal/records"
)

// tempFiles returns the names in dir that look like the managed file's
// temporary files.
func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".dnsmasq-manager.hosts.*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// readFile returns the contents of path.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPath(t *testing.T) {
	f := hostsfile.File{Dir: "/srv/hosts"}
	if got, want := f.Path(), filepath.Join("/srv/hosts", "dnsmasq-manager.hosts"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	f := hostsfile.File{Dir: t.TempDir()}
	s, err := f.Load(policy())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !s.Equal(records.State{}) {
		t.Errorf("Load = %+v, want empty", s)
	}
	if _, err := os.Stat(f.Path()); !os.IsNotExist(err) {
		t.Errorf("managed file exists after Load: %v", err)
	}
}

func TestSaveThenLoad(t *testing.T) {
	f := hostsfile.File{Dir: t.TempDir()}
	if err := f.Save(exampleState()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(f.Path())
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode(); mode != 0o644 {
		t.Errorf("mode = %v, want 0644", mode)
	}
	if left := tempFiles(t, f.Dir); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
	s, err := f.Load(policy())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !s.Equal(exampleState()) {
		t.Errorf("Load = %+v, want %+v", s, exampleState())
	}
}

func TestSaveReplacesAtomically(t *testing.T) {
	f := hostsfile.File{Dir: t.TempDir()}
	second := exampleState()
	second.Records = second.Records[2:]
	for _, s := range []records.State{exampleState(), second} {
		if err := f.Save(s); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	if got, want := readFile(t, f.Path()), string(hostsfile.Render(second)); got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
	if left := tempFiles(t, f.Dir); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestSaveEmptyStateWritesHeader(t *testing.T) {
	f := hostsfile.File{Dir: t.TempDir()}
	if err := f.Save(records.State{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := readFile(t, f.Path()); got != header {
		t.Errorf("file = %q, want %q", got, header)
	}
}

func TestLoadErrorNamesPathAndLine(t *testing.T) {
	f := hostsfile.File{Dir: t.TempDir()}
	bad := header + "10.0.0.80 app.home.example.test\n"
	if err := os.WriteFile(f.Path(), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := f.Load(policy())
	if err == nil {
		t.Fatal("Load: err = nil, want an error")
	}
	if prefix := f.Path() + ":3: "; !strings.HasPrefix(err.Error(), prefix) {
		t.Errorf("err = %q, want prefix %q", err, prefix)
	}
	if got := readFile(t, f.Path()); got != bad {
		t.Errorf("file changed by Load: %q", got)
	}
}

func TestLoadRemovesStaleTemps(t *testing.T) {
	f := hostsfile.File{Dir: t.TempDir()}
	for _, name := range []string{".dnsmasq-manager.hosts.123", ".dnsmasq-manager.hosts.health-456", "other.hosts", ".other"} {
		if err := os.WriteFile(filepath.Join(f.Dir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.Load(policy()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if left := tempFiles(t, f.Dir); len(left) != 0 {
		t.Errorf("stale temporary files left: %v", left)
	}
	for _, name := range []string{"other.hosts", ".other"} {
		if got := readFile(t, filepath.Join(f.Dir, name)); got != "x\n" {
			t.Errorf("%s = %q, want untouched", name, got)
		}
	}
}

func TestSaveSucceedsWhenDirSyncFailsAfterRename(t *testing.T) {
	f := hostsfile.File{Dir: t.TempDir()}
	restore := hostsfile.SetSyncDir(func(string) error { return errors.New("sync failed") })
	defer restore()

	if err := f.Save(exampleState()); err != nil {
		t.Fatalf("Save = %v, want nil once the rename has replaced the file", err)
	}
	got, err := f.Load(policy())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Equal(exampleState()) {
		t.Errorf("Load = %+v, want the saved state", got)
	}
	if left := tempFiles(t, f.Dir); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestSaveFailsWhenDirMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hosts")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	f := hostsfile.File{Dir: dir}
	if err := f.Save(exampleState()); err == nil {
		t.Fatal("Save: err = nil, want an error")
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(exampleState()); err != nil {
		t.Fatalf("Save after the directory is back: %v", err)
	}
}

func TestLoadFailsWhenDirMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hosts")
	_, err := hostsfile.File{Dir: dir}.Load(policy())
	if err == nil {
		t.Fatal("Load: err = nil, want an error")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("err = %q, want it to name %s", err, dir)
	}
}
