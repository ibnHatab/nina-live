package core

import (
	"sync"

	"github.com/ibnHatab/nina-live/strain"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
)

// Strain is an API error of one of her services (source: mind, voice, ears, search) read as
// what it says about her: busy, tired, offline (strain.Kind), a world event (strain) linked to
// Nina, projected through the quantized port (her voice and her tasks hear a change of kind or
// of confidence, once) and offered to the domains as strain(S, K). An error that says none of
// these is not a strain (nil: nothing). A nil error is a success: well again, if she was strained.
func (k *Core) Strain(source string, err error) {
	if err == nil {
		k.strainWell(source)
		return
	}
	kind := strain.Kind(err.Error())
	if kind == "" || k.World == nil {
		return
	}
	k.strainMu.Lock()
	if k.strained == nil {
		k.strained = map[string]bool{}
	}
	k.strained[source] = true
	k.strainMu.Unlock()
	k.World.Apply(semhome.Event{At: k.Cfg.Now(), Kind: "strain", ID: source, Value: kind, Ref: strain.Gist(err.Error())})
}

func (k *Core) strainWell(source string) {
	k.strainMu.Lock()
	was := k.strained[source]
	delete(k.strained, source)
	k.strainMu.Unlock()
	if was && k.World != nil { // only after a strain: a success is not news otherwise
		k.World.Apply(semhome.Event{At: k.Cfg.Now(), Kind: "strain", ID: source, Value: "well"})
	}
}

// strainState is guarded apart from k.mu (an error may come from inside a call that holds it).
type strainState struct {
	strainMu sync.Mutex
	strained map[string]bool
}
