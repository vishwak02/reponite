package main

// installFacts installs the derived-result cache when the build has one
// (factcache.go); a build without it computes every answer.
var installFacts func()

func installFactsIfBuilt() {
	if installFacts != nil {
		installFacts()
	}
}
