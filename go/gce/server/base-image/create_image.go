package main

import (
	"flag"
	"path"
	"path/filepath"
	"runtime"

	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/gce"
	"go.skia.org/infra/go/sklog"
)

const (
	IMAGE_DESCRIPTION = "Base image for all Skia servers."
	IMAGE_FAMILY      = "skia-pushable-base"
	INSTANCE_NAME     = "skia-pushable-base-maker"
	SETUP_SCRIPT      = "~/setup_script.sh"
)

var (
	// Flags.
	workdir = flag.String("workdir", ".", "Working directory.")
)

func BaseConfig() *gce.Instance {
	// The setup script has to be run in an interactive terminal. Make sure
	// it ends up on the machine and we'll ask the user to run it.
	_, filename, _, _ := runtime.Caller(0)
	dir := path.Dir(filename)

	vm := &gce.Instance{
		BootDisk: &gce.Disk{
			Name:        INSTANCE_NAME,
			SourceImage: "projects/debian-cloud/global/images/debian-9-stretch-v20170829",
			Type:        gce.DISK_TYPE_PERSISTENT_STANDARD,
		},
		MachineType: gce.MACHINE_TYPE_STANDARD_4,
		Name:        INSTANCE_NAME,
		Os:          gce.OS_LINUX,
		Scopes: []string{
			"https://www.googleapis.com/auth/cloud-platform",
		},
		SetupScript: path.Join(dir, "setup-script.sh"),
		User:        gce.USER_DEFAULT,
	}

	return vm
}

func main() {
	common.Init()
	defer common.LogPanic()

	// Get the absolute workdir.
	wdAbs, err := filepath.Abs(*workdir)
	if err != nil {
		sklog.Fatal(err)
	}

	// Create the GCloud object.
	g, err := gce.NewGCloud(gce.ZONE_DEFAULT, wdAbs)
	if err != nil {
		sklog.Fatal(err)
	}

	vm := BaseConfig()

	// Delete the instance if it already exists, to ensure that we're in a
	// clean state.
	if err := g.Delete(vm, true, true); err != nil {
		sklog.Fatal(err)
	}

	// Create/Setup the instance.
	if err := g.CreateAndSetup(vm, false); err != nil {
		sklog.Fatal(err)
	}

	// Capture the image.
	if err := g.CaptureImage(vm, IMAGE_FAMILY, IMAGE_DESCRIPTION); err != nil {
		sklog.Fatal(err)
	}
}
