// Package services specifies all the different services the gold-server command
// can run.
package services

import (
	"slices"

	"go.goldmine.build/go/skerr"
)

type Service string

// All the services as constants.
const (
	Baseline Service = "baseline"
	DiffCalc Service = "diffcalc"
	Frontend Service = "frontend"
	Ingester Service = "ingester"
	Periodic Service = "periodic"
)

var AllServices []Service = []Service{
	Baseline,
	DiffCalc,
	Frontend,
	Ingester,
	Periodic,
}

// Validate takes in a list from command line flags and confirms each service
// name is a valid value.
func Validate(flags []string) ([]Service, error) {
	ret := []Service{}

	for _, f := range flags {
		fAsService := Service(f)
		if slices.Contains(AllServices, fAsService) {
			ret = append(ret, fAsService)
		} else {
			return nil, skerr.Fmt("%s is not a valid service, not one of %q", fAsService, AllServices)
		}
	}
	if len(ret) == 0 {
		ret = AllServices
	}
	return ret, nil
}
