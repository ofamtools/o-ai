package main

import "ofam.dev/ochazuke"

// Namespace is the singular namespace o-ai declares (lexicon plan): its
// operations are ai.<noun>.<verb> and a host mounts its catalog under it.
const Namespace = "ai"

func init() {
	if err := ochazuke.DeclareNamespace(Namespace); err != nil {
		panic(err)
	}
}
