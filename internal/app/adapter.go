package app

import (
	"fmt"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/knulli"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
)

// deviceAdapter is the adapter of the firmware, with the battery the
// status reads.
type deviceAdapter interface {
	osadapter.Adapter
	Battery() *osadapter.Battery
}

// newAdapter returns the adapter of opts.OS (milestone M20).
func newAdapter(opts Options) (deviceAdapter, error) {
	switch opts.OS {
	case "", nextui.OSName:
		return nextui.New(opts.Roots, opts.Env), nil
	case knulli.OSName:
		return knulli.New(opts.Roots, opts.Env), nil
	}
	return nil, fmt.Errorf("unknown OS %q (nextui or knulli)", opts.OS)
}
