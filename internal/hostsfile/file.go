package hostsfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gsdevme/dnsmasq-manager/internal/records"
)

// tempPrefix starts the name of every temporary file Save writes. dnsmasq
// ignores dotfiles in a hosts directory, so it never reads a partial file.
const tempPrefix = "." + FileName + "."

// File is the managed file in the hosts directory Dir. It touches no other
// file in Dir except its own leftover temporary files.
type File struct {
	Dir string
}

// Path returns the managed file's path, Dir/dnsmasq-manager.hosts.
func (f File) Path() string {
	return filepath.Join(f.Dir, FileName)
}

// Load is for start-up. It removes temporary files a crashed Save left
// behind, then reads and parses the managed file against policy. A missing
// file is an empty state and nothing is written. A file that does not parse
// is left alone and gives an error reading "path:line: reason" that wraps the
// *ParseError. A missing or unreadable directory is an error naming it.
func (f File) Load(policy records.Policy) (records.State, error) {
	if err := f.removeStaleTemps(); err != nil {
		return records.State{}, err
	}
	data, err := os.ReadFile(f.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return records.State{}, nil
	}
	if err != nil {
		return records.State{}, fmt.Errorf("reading the managed file: %w", err)
	}
	s, err := Parse(data, policy)
	if err != nil {
		return records.State{}, fmt.Errorf("%s:%w", f.Path(), err)
	}
	return s, nil
}

// removeStaleTemps deletes every regular file in Dir whose name starts with
// tempPrefix.
func (f File) removeStaleTemps() error {
	entries, err := os.ReadDir(f.Dir)
	if err != nil {
		return fmt.Errorf("reading the hosts directory: %w", err)
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasPrefix(e.Name(), tempPrefix) {
			continue
		}
		if err := os.Remove(filepath.Join(f.Dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing a stale temporary file: %w", err)
		}
	}
	return nil
}

// Save writes Render(s) to the managed file atomically, with mode 0644: it
// writes and syncs a temporary file in Dir, renames it over the managed file
// and syncs Dir. A failure before the rename leaves the managed file
// unchanged, removes the temporary file and is returned. Once the rename has
// succeeded the write has taken effect and dnsmasq may already serve it, so
// Save returns nil: the directory sync after it is best effort, and its
// failure only weakens durability across a power loss. An empty state still
// writes the header. Save satisfies records.Saver.
func (f File) Save(s records.State) (err error) {
	tmp, err := os.CreateTemp(f.Dir, tempPrefix+"*")
	if err != nil {
		return fmt.Errorf("creating a temporary file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(Render(s)); err != nil {
		return fmt.Errorf("writing %s: %w", tmp.Name(), err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("setting the mode of %s: %w", tmp.Name(), err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("syncing %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), f.Path()); err != nil {
		return fmt.Errorf("replacing the managed file: %w", err)
	}
	_ = syncDir(f.Dir)
	return nil
}

// syncDir flushes dir's entries, so a rename into it survives a crash. It is
// a variable so tests can make the sync fail.
var syncDir = func(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening the hosts directory: %w", err)
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return fmt.Errorf("syncing the hosts directory: %w", err)
	}
	if err := d.Close(); err != nil {
		return fmt.Errorf("closing the hosts directory: %w", err)
	}
	return nil
}
