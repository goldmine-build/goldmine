package main

import (
	"context"
	"fmt"
	"os"
	"path"

	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/gitiles"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
)

const (
	PROTO_FILE_PATH = "third_party/cq_client/cq.proto"
	PROTO_REPO      = "https://chromium.googlesource.com/chromium/tools/depot_tools"
)

// findCheckoutRoot attempts to find the root of the checkout, assuming that
// this program is being run from somewhere inside the checkout.
func findCheckoutRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for cwd != "." && cwd != "/" {
		if _, err := os.Stat(path.Join(cwd, ".git")); err == nil {
			return cwd, nil
		}
		cwd = path.Dir(cwd)
	}
	return "", fmt.Errorf("Unable to find checkout root.")
}

func main() {
	common.Init()
	defer common.LogPanic()

	root, err := findCheckoutRoot()
	if err != nil {
		sklog.Fatal(err)
	}
	cqDir := path.Join(root, "go", "cq")
	dst := path.Join(cqDir, path.Base(PROTO_FILE_PATH))

	// Download the most recent version of the proto file.
	if err := gitiles.NewRepo(PROTO_REPO, nil).DownloadFile(PROTO_FILE_PATH, dst); err != nil {
		sklog.Fatal(err)
	}
	defer util.Remove(dst)

	// Regenerate project_cfg.pb.go from the .proto file.
	if output, err := exec.RunCwd(context.Background(), cqDir, "protoc", "--go_out=plugins=grpc:.", dst, "--proto_path", cqDir); err != nil {
		sklog.Fatalf("Error: %s\n\n%s", err, output)
	}
}
