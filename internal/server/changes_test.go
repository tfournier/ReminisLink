package server

import (
	"net/http"
	"sync"
	"testing"

	"github.com/tfournier/ReminisLink/internal/library"
)

// changeLog collects the OnChange calls.
type changeLog struct {
	mu  sync.Mutex
	all []Change
}

func (c *changeLog) add(ch Change) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.all = append(c.all, ch)
}

func (c *changeLog) take() []Change {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.all
	c.all = nil
	return out
}

func TestOnChange(t *testing.T) {
	log := &changeLog{}
	f := newFixture(t, func(o *Options) { o.OnChange = log.add })

	// A ROM upload names the file.
	data := []byte("tiny rom")
	target := map[string]any{"kind": "rom", "systemId": library.EncodeID(gbaFolder), "fileName": "Moon Run.gba"}
	c, _ := f.createUpload(map[string]any{"target": target, "sizeBytes": len(data), "sha256": sha(data)}, http.StatusCreated)
	if r := f.chunk(c.UploadID, 0, data); r.Status != http.StatusNoContent {
		t.Fatalf("chunk: %d", r.Status)
	}
	if got := log.take(); len(got) != 0 {
		t.Fatalf("change before complete: %+v", got)
	}
	f.do(http.MethodPost, "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, http.StatusOK)
	if got := log.take(); len(got) != 1 || got[0] != (Change{Kind: ChangeROMAdded, Name: "Moon Run.gba"}) {
		t.Fatalf("upload: %+v", got)
	}

	// A deleted game is named from before the deletion.
	f.do(http.MethodDelete, gamePath(gbaFolder+"/Moon Run.gba"), "/games/{gameId}", nil, http.StatusOK)
	if got := log.take(); len(got) != 1 || got[0] != (Change{Kind: ChangeGameDeleted, Name: "Moon Run"}) {
		t.Fatalf("delete: %+v", got)
	}

	// A failed request changes nothing.
	f.do(http.MethodDelete, gamePath(gbaFolder+"/Missing.gba"), "/games/{gameId}", nil, http.StatusNotFound)
	if got := log.take(); len(got) != 0 {
		t.Fatalf("failed request: %+v", got)
	}

	// Emptying the trash.
	f.send(http.MethodDelete, "/api/v1/trash", "/trash", nil, map[string]string{"X-RL-Confirm": "empty-trash"}, http.StatusOK)
	if got := log.take(); len(got) != 1 || got[0].Kind != ChangeTrashEmptied {
		t.Fatalf("trash: %+v", got)
	}
}

func TestSetDeviceName(t *testing.T) {
	f := newFixture(t)
	f.server.SetDeviceName("Brick du salon")
	r := f.unsigned("GET", "/api/v1/hello", nil)
	var h helloResponse
	r.JSON(t, &h)
	if h.DeviceName != "Brick du salon" || f.server.DeviceName() != "Brick du salon" {
		t.Fatalf("hello after rename: %s", r.Body)
	}
}
