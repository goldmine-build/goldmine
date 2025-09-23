package main

import (
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/kube/go/oauth2redirect"
)

func main() {
	sklog.Fatal(oauth2redirect.Main())
}
