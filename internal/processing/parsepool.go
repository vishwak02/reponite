// parsepool.go parses source files on every core while a single walker reads
// them. Reading a git tree is sequential (go-git is not safe for concurrent
// use), but parsing and extraction are pure per file and dominate indexing
// time; the pool keeps the walk order, so the index is identical to a serial
// one.
package processing

import (
	"runtime"
	"sync"
)

type parsePool struct {
	slots []*ParsedFile // walk order; a nil or path-less slot is dropped
	jobs  chan func()
	wg    sync.WaitGroup
	mu    sync.Mutex
	err   error
}

func newParsePool() *parsePool {
	n := runtime.GOMAXPROCS(0)
	p := &parsePool{jobs: make(chan func(), 2*n)}
	for i := 0; i < n; i++ {
		go func() {
			for j := range p.jobs {
				j()
				p.wg.Done()
			}
		}()
	}
	return p
}

// add keeps an already-built file in walk order.
func (p *parsePool) add(pf ParsedFile) { p.slots = append(p.slots, &pf) }

// submit reserves the next slot and fills it on a worker; ok=false drops it.
func (p *parsePool) submit(parse func() (ParsedFile, bool, error)) {
	slot := &ParsedFile{}
	p.slots = append(p.slots, slot)
	p.wg.Add(1)
	p.jobs <- func() {
		pf, ok, err := parse()
		if err != nil {
			p.mu.Lock()
			if p.err == nil {
				p.err = err
			}
			p.mu.Unlock()
			return
		}
		if ok {
			*slot = pf
		}
	}
}

// wait finishes every job and returns the files in walk order.
func (p *parsePool) wait() ([]ParsedFile, error) {
	p.wg.Wait()
	close(p.jobs)
	out := make([]ParsedFile, 0, len(p.slots))
	for _, s := range p.slots {
		if s.Path != "" {
			out = append(out, *s)
		}
	}
	return out, p.err
}
