package adapter

import "github.com/onsi/gomega/gexec"
import . "github.com/onsi/ginkgo"
import . "github.com/onsi/gomega"

var _ = Describe("adapter executable", func() {
	It("conforms to the sdk interface, and is buildable", func() {
		_, err := gexec.Build("github.com/pivotal-cf-experimental/redis-example-service-adapter/cmd/service-adapter")
		Expect(err).ToNot(HaveOccurred())
	})
})
