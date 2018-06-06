package adapter_test

import (
	"errors"
	"log"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"github.com/pivotal-cf-experimental/redis-example-service-adapter/adapter"

	"github.com/pivotal-cf/on-demand-services-sdk/bosh"
	"github.com/pivotal-cf/on-demand-services-sdk/serviceadapter"
)

var _ = Describe("Create binding", func() {
	var (
		bindingID = "binding-id"
		topology  = bosh.BoshVMs{
			"redis-server": []string{"127.0.0.1"},
		}
		params = serviceadapter.RequestParameters{
			"context": map[string]interface{}{
				"platform": "cloudfoundry",
			},
		}

		manifest bosh.BoshManifest
		binder   = adapter.Binder{
			StderrLogger: log.New(GinkgoWriter, "create-binding", log.LstdFlags),
		}
	)

	BeforeEach(func() {
		manifest = bosh.BoshManifest{
			InstanceGroups: []bosh.InstanceGroup{
				bosh.InstanceGroup{
					Properties: map[string]interface{}{
						"redis": map[interface{}]interface{}{
							"password": "supersecret",
						},
					},
				},
			},
		}
	})

	DescribeTable("binding",
		func(secretPath string, resolvedSecrets serviceadapter.ManifestSecrets, secretInBinding string, expectedErr error) {
			if secretPath != "" {
				properties := manifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})
				properties["secret"] = secretPath
			}
			binding, err := binder.CreateBinding(bindingID, topology, manifest, params, resolvedSecrets)
			if expectedErr != nil {
				Expect(err).To(MatchError(expectedErr))
				return
			}
			Expect(err).NotTo(HaveOccurred())
			s, ok := binding.Credentials["secret"]
			Expect(ok).To(BeTrue(), "secret not found in binding.Credentials")
			Expect(s).To(Equal(secretInBinding))
		},
		Entry("without secrets", "", serviceadapter.ManifestSecrets{}, "", nil),
		Entry("with ((foo)) resolved by the broker", "((foo))", serviceadapter.ManifestSecrets{"foo": "{\"status\": \"bar\"}"}, "{\"status\": \"bar\"}", nil),
		Entry("with ((foo)) not resolved by the broker", "((foo))", serviceadapter.ManifestSecrets{}, "", errors.New("secret 'foo' not present in manifest secrets passed to bind")),
		Entry("with malformed path: (())", "(())", serviceadapter.ManifestSecrets{}, "", errors.New("expecting a credhub ref string with format ((xxx)), but got: (())")),
		Entry("with malformed path: ((foo))((bar))", "((foo))((bar))", serviceadapter.ManifestSecrets{}, "", errors.New("expecting a credhub ref string with format ((xxx)), but got: ((foo))((bar))")),
		Entry("with malformed path: foo", "foo", serviceadapter.ManifestSecrets{}, "", errors.New("expecting a credhub ref string with format ((xxx)), but got: foo")),
	)

	It("catches a non-string secret value in the manifest", func() {
		properties := manifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})
		properties["secret"] = 73
		resolvedSecrets := serviceadapter.ManifestSecrets{}
		_, err := binder.CreateBinding(bindingID, topology, manifest, params, resolvedSecrets)
		Expect(err).To(MatchError(errors.New("secret in manifest was not a string. expecting a credhub ref string")))
	})
})
