package adapter_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pivotal-cf-experimental/redis-example-service-adapter/adapter"
	"github.com/pivotal-cf/on-demand-services-sdk/bosh"
	"github.com/pivotal-cf/on-demand-services-sdk/serviceadapter"
)

var _ = Describe("DashboardGenerator", func() {
	It("returns a dashboard url", func() {
		generator := adapter.DashboardGenerator{}

		dashboard, err := generator.DashboardUrl(serviceadapter.DashboardUrlParams{
			InstanceID: "some-instance-id",
			Plan:       serviceadapter.Plan{},
			Manifest:   bosh.BoshManifest{},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(dashboard.DashboardUrl).To(Equal("https://example.com/dashboard/some-instance-id"))
	})
})
