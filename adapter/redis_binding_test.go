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
							"password":                 "supersecret",
							adapter.GeneratedSecretKey: path(adapter.GeneratedSecretKey),
							adapter.ManagedSecretKey:   path(adapter.ManagedSecretKey),
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
			binding, err := binder.CreateBinding(bindingID, topology, manifest, params, resolvedSecrets, serviceadapter.DNSAddresses{})
			if expectedErr != nil {
				Expect(err).To(MatchError(expectedErr))
				return
			}
			Expect(err).NotTo(HaveOccurred())
			s, ok := binding.Credentials["secret"]
			Expect(ok).To(BeTrue(), "secret not found in binding.Credentials")
			Expect(s).To(Equal(secretInBinding))
		},
		Entry("without secrets in the manifest", "", nil, "", nil),
		Entry("with ((foo)) resolved by the broker", "((foo))", secretsMap(defaultMap(), "((foo))", "g1"), "g1", nil),
		Entry("with ((foo)) not resolved by the broker", "((foo))", defaultMap(), "", errors.New("secret 'foo' not present in manifest secrets passed to bind")),
		Entry("with malformed path: (())", "(())", defaultMap(), "", errors.New("expecting a credhub ref string with format ((xxx)), but got: (())")),
		Entry("with malformed path: ((foo))((bar))", "((foo))((bar))", defaultMap(), "", errors.New("expecting a credhub ref string with format ((xxx)), but got: ((foo))((bar))")),
		Entry("with malformed path: foo", "foo", defaultMap(), "", errors.New("expecting a credhub ref string with format ((xxx)), but got: foo")),
		Entry("with secret_pass not being interpolated", "", serviceadapter.ManifestSecrets{}, "", errors.New("manifest wasn't correctly interpolated: missing value for `"+adapter.GeneratedSecretKey+"`")),
		Entry("with managed_secret not being interpolated", "((foo))", serviceadapter.ManifestSecrets{path(adapter.GeneratedSecretKey): "p1"}, "", errors.New("manifest wasn't correctly interpolated: missing value for `"+adapter.ManagedSecretKey+"`")),
	)

	Describe("binding with DNS", func() {
		It("produces a binding containing a DNS address", func() {
			dnsAddresses := serviceadapter.DNSAddresses{"config-1": "this.is.a.dns.address"}
			binding, err := binder.CreateBinding(bindingID, topology, manifest, params, defaultMap(), dnsAddresses)
			Expect(err).NotTo(HaveOccurred())
			s, ok := binding.Credentials["dns_addresses"]
			Expect(ok).To(BeTrue(), "DNS address not found in binding.Credentials")
			Expect(s).To(Equal(dnsAddresses))
		})
	})
})

func path(s string) string {
	return "((" + s + "))"
}

func defaultMap() serviceadapter.ManifestSecrets {
	return serviceadapter.ManifestSecrets{
		path(adapter.GeneratedSecretKey): "value1",
		path(adapter.ManagedSecretKey):   "value2",
	}
}

func secretsMap(m serviceadapter.ManifestSecrets, key, value string) serviceadapter.ManifestSecrets {
	m[key] = value
	return m
}
