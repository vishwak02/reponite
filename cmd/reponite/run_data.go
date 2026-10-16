//go:build sqlite && treesitter

package main

import (
	"flag"
	"fmt"

	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/query"
)

// dataFlags are the flags `models` and `config` share.
type dataFlags struct {
	local  bool
	budget int
	kind   string
	format string
	repo   string
	rf     refFlags
}

func (d *dataFlags) register(fs *flag.FlagSet, kinds string) {
	fs.BoolVar(&d.local, "local", false, "scan only this repo instead of the registered fleet")
	fs.IntVar(&d.budget, "budget", 0, "token budget (0 = default ~4000); a cut result says truncated + next")
	fs.StringVar(&d.kind, "kind", "", "keep one kind: "+kinds)
	fs.StringVar(&d.format, "format", "json", "json or text")
	fs.StringVar(&d.repo, "repo", "", "scope to one repo (default: the fleet)")
	d.rf.register(fs)
	registerNoTests(fs)
}

// positionalRef splits `<name> [ref]`: a lone positional that is an indexed
// ref (and not HEAD) is the ref, not a name.
func positionalRef(f *fleetStore, pos []string, d *dataFlags) string {
	name := arg(pos, 0, "")
	if len(pos) >= 2 {
		d.rf.ref = pos[1]
	} else if name != "" && name != "HEAD" {
		for _, r := range f.Repos() {
			for _, x := range f.Refs(r) {
				if x == name {
					d.rf.ref = name
					return ""
				}
			}
		}
	}
	return name
}

// cmdModels is the data layer: an ER summary, or one model's fields,
// relations, serializers, routes, readers and writers.
func cmdModels(args []string) {
	var d dataFlags
	pos := parseCmd("models", "models [model|table] [ref] [--budget N] [--kind table|view|abstract|schema|wire] [--format text] [--repo R] [--ref R] [--refs repo=ref,...] [--local] [--no-tests]", args, func(fs *flag.FlagSet) {
		d.register(fs, "table|view|abstract|schema|wire")
	})
	f := openFleet(d.local)
	defer f.Close()
	name := positionalRef(f, pos, &d)
	s, cov := f.view(d.rf.ref, d.rf.pins)
	repo := query.FleetRepo
	if d.repo != "" {
		repo = d.repo
	}
	res := query.Models(s, repo, d.rf.ref, name, query.ModelsOptions{Budget: d.budget, Kind: d.kind})
	res.Note = joinNote(res.Note, cov)
	if d.format == "text" {
		fmt.Print(interfaces.ModelsText(res))
		return
	}
	printJSON(interfaces.ModelsJSON(res))
}

// cmdConfig is the configuration surface: every key read (with defaults)
// and set, keys read-never-set and set-never-read; or one key's sites.
func cmdConfig(args []string) {
	var d dataFlags
	pos := parseCmd("config", "config [key] [ref] [--budget N] [--kind env|flag|setting|feature|rosparam] [--format text] [--repo R] [--ref R] [--refs repo=ref,...] [--local] [--no-tests]", args, func(fs *flag.FlagSet) {
		d.register(fs, "env|flag|setting|feature|rosparam")
	})
	f := openFleet(d.local)
	defer f.Close()
	key := positionalRef(f, pos, &d)
	s, cov := f.view(d.rf.ref, d.rf.pins)
	repo := query.FleetRepo
	if d.repo != "" {
		repo = d.repo
	}
	res := query.Config(s, repo, d.rf.ref, key, query.ConfigOptions{Budget: d.budget, Kind: d.kind})
	res.Note = joinNote(res.Note, cov)
	if d.format == "text" {
		fmt.Print(interfaces.ConfigText(res))
		return
	}
	printJSON(interfaces.ConfigJSON(res))
}
