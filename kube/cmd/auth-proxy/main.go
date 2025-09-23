package main

import (
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/kube/go/authproxy"
)

func main() {
	sklog.Fatal(authproxy.Main())
}
