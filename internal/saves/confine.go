package saves

import (
	"os"

	"github.com/tfournier/ReminisLink/internal/confine"
)

// errOutside means a file is a symbolic link, is reached through one, or is
// not under the saves folder: it is never read (rule 2 of CLAUDE.md).
var errOutside = confine.ErrOutside

// relToRoot returns p relative to dir, errOutside when it is not under it.
func relToRoot(dir, p string) (string, error) { return confine.RelToRoot(dir, p) }

// openConfined opens the regular file rel of root (the saves folder) for
// reading, refusing links and escapes (confine.Open).
var openConfined = confine.Open

// openSavesFile opens the file p (an absolute path given by the adapter)
// with openConfined under the saves folder of the adapter.
func (s *Service) openSavesFile(p string) (*os.File, error) {
	f, _, err := confine.OpenUnder(s.o.Adapter.Roots().Saves, p)
	return f, err
}

// checkWriteDir refuses a read or a write in dir when a folder between the root of
// the card (or the saves folder, when it is elsewhere) and dir is a
// symbolic link (rule 2): the write would land elsewhere on the volume.
func (s *Service) checkWriteDir(dir string) error {
	r := s.o.Adapter.Roots()
	root := r.Saves
	if rel, err := confine.RelToRoot(r.SD, r.Saves); r.SD != "" && err == nil && rel != "" {
		root = r.SD
	}
	return confine.CheckDir(root, dir)
}
