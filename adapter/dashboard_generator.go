package adapter

import (
	"fmt"
	"github.com/pivotal-cf/on-demand-services-sdk/serviceadapter"
)

type DashboardGenerator struct{}

func (d DashboardGenerator) DashboardUrl(params serviceadapter.DashboardUrlParams) (serviceadapter.DashboardUrl, error) {
	return serviceadapter.DashboardUrl{
		DashboardUrl: fmt.Sprintf("https://example.com/dashboard/%s", params.InstanceID),
	}, nil
}
