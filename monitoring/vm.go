package main

import (
	"go.skia.org/infra/go/gce"
	"go.skia.org/infra/go/gce/server"
)

func MonitoringBase(name, ipAddress string) *gce.Instance {
	vm := server.Server20170928(name)
	vm.DataDisk.SizeGb = 1000
	vm.DataDisk.Type = gce.DISK_TYPE_PERSISTENT_STANDARD
	vm.ExternalIpAddress = ipAddress
	vm.MachineType = gce.MACHINE_TYPE_HIGHMEM_16
	vm.Metadata["owner_primary"] = "jcgregorio"
	vm.Metadata["owner_secondary"] = "borenet"
	return vm
}

func Prod() *gce.Instance {
	return MonitoringBase("skia-monitoring", "35.202.138.145" /* Whitelisted in skia-master-db cloud DB */)
}

func Staging() *gce.Instance {
	return MonitoringBase("skia-monitoring-staging", "35.193.5.196")
}

func main() {
	server.Main(gce.ZONE_DEFAULT, map[string]*gce.Instance{
		"prod":    Prod(),
		"staging": Staging(),
	})
}
