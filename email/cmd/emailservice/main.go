package main

import (
	"go.goldmine.build/email/go/emailservice"
	"go.goldmine.build/go/sklog"
)

func main() {
	sklog.Fatal(emailservice.Main())
}
