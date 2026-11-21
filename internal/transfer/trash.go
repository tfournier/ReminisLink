package transfer

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/tfournier/ReminisLink/internal/paks"
	"github.com/tfournier/ReminisLink/internal/store"
)

// TrashRestoreResult is the outcome of RestoreTrash.
type TrashRestoreResult struct {
	store.RestoreResult
	// PakRestored: a pak folder came back (a paks event follows).
	PakRestored bool `json:"-"`
}

// RestoreTrash puts a trash entry back (M18, POST
// /trash/{entryId}/restore) under the install lock: onConflict is
// store.RestoreFail or store.RestoreRename. A folder going back to a
// folder of the paks goes through the checks of an installation
// (paks.CheckFolder: 422 unsupported_format when refused).
func (s *Service) RestoreTrash(id, onConflict string) (TrashRestoreResult, *Error, error) {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	pak := false
	res, err := s.opts.Trash.Restore(id, onConflict, func(p store.RestorePlan) error {
		for _, it := range p.Items {
			if !it.Dir || it.Internal || s.paks == nil || s.paks.KindOf(filepath.Dir(it.Dst)) == "" {
				continue
			}
			if err := paks.CheckFolder(context.Background(), it.Src); err != nil {
				return err
			}
			pak = true
		}
		return nil
	})
	var conflict *store.RestoreConflictError
	switch {
	case err == nil:
	case errors.Is(err, store.ErrInvalidTrashID):
		return TrashRestoreResult{}, newError(http.StatusBadRequest, CodeInvalidID, "Invalid identifier"), nil
	case errors.Is(err, store.ErrTrashNotFound), errors.Is(err, store.ErrNothingToMove):
		return TrashRestoreResult{}, notFound("Trash entry not found"), nil
	case errors.As(err, &conflict):
		return TrashRestoreResult{}, &Error{Status: http.StatusConflict, Code: CodeConflict,
			Message: "A file exists where the entry goes back", Details: map[string]any{"conflicts": conflict.Conflicts}}, nil
	default:
		if fe, ok := paks.IsFormatError(err); ok {
			return TrashRestoreResult{}, newError(http.StatusUnprocessableEntity, CodeUnsupportedFormat, fe.Reason), nil
		}
		return TrashRestoreResult{}, nil, err
	}
	return TrashRestoreResult{RestoreResult: res, PakRestored: pak}, nil, nil
}
