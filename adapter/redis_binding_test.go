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

var _ = Describe("Binding", func() {

	Describe("Create binding", func() {
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
								"ca_cert":                  "((instance_certificate.ca))",
								"private_key":              "((instance_certificate.private_key))",
								"certificate":              "((instance_certificate.certificate))",
								"secret":                   "((default_secret))",
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
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(Equal(expectedErr.Error()))
					return
				}
				Expect(err).NotTo(HaveOccurred())
				s, ok := binding.Credentials["secret"]
				Expect(ok).To(BeTrue(), "secret not found in binding.Credentials")
				Expect(s).To(Equal(secretInBinding))
			},
			Entry("without secrets in the manifest", "", nil, "", nil),
			Entry("with ((secret)) resolved by the broker", "((secret))", secretsMap(defaultMap(), "((secret))", "g1"), "g1", nil),
			Entry("with ((foo)) not resolved by the broker", "((foo))", defaultMap(), "", errors.New("manifest wasn't correctly interpolated: missing value for `((foo))`")),
			Entry("with malformed path: (())", "(())", defaultMap(), "", errors.New("expecting a credhub ref string with format ((xxx)), but got: (())")),
			Entry("with malformed path: foo", "foo", defaultMap(), "", errors.New("expecting a credhub ref string with format ((xxx)), but got: foo")),
			Entry("with secret_pass not being interpolated", "", serviceadapter.ManifestSecrets{}, "", errors.New("manifest wasn't correctly interpolated: missing value for `(("+adapter.GeneratedSecretKey+"))`")),
			Entry("with managed_secret not being interpolated", "", serviceadapter.ManifestSecrets{path(adapter.GeneratedSecretKey): "p1"}, "", errors.New("manifest wasn't correctly interpolated: missing value for `(("+adapter.ManagedSecretKey+"))`")),
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

	Describe("Delete Binding", func() {
		var (
			binder adapter.Binder
		)

		BeforeEach(func() {
			binder = adapter.Binder{
				StderrLogger: log.New(GinkgoWriter, "create-binding", log.LstdFlags),
			}
		})

		Describe("secure manifests are enabled", func() {
			BeforeEach(func() {
				binder.Config.SecureManifestsEnabled = true
			})
			It("returns an error when it doesn't have the right credentials", func() {
				badSecretKey := "unwanted-secret"
				Expect(badSecretKey).ToNot(Equal(adapter.GeneratedSecretVariableName))
				secrets := serviceadapter.ManifestSecrets{"((" + badSecretKey + "))": "not the password"}

				err := binder.DeleteBinding("binding-id", nil, bosh.BoshManifest{}, nil, secrets)
				Expect(err).To(MatchError("The required secret was not provided to DeleteBinding"))
			})

			It("returns an error when the provided credential is empty", func() {
				secrets := serviceadapter.ManifestSecrets{"((" + adapter.GeneratedSecretVariableName + "))": ""}

				err := binder.DeleteBinding("binding-id", nil, bosh.BoshManifest{}, nil, secrets)
				Expect(err).To(MatchError("The incorrect secret value was provided to DeleteBinding"))
			})

			It("doesn't return an error when it has the right credentials", func() {
				secrets := serviceadapter.ManifestSecrets{"((" + adapter.GeneratedSecretVariableName + "))": "bosh generated passw0rd"}

				err := binder.DeleteBinding("binding-id", nil, bosh.BoshManifest{}, nil, secrets)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("secure manifests are disabled", func() {
			BeforeEach(func() {
				binder.Config.SecureManifestsEnabled = false
			})
			It("does not error when no secrets are passed", func() {

				err := binder.DeleteBinding("binding-id", nil, bosh.BoshManifest{}, nil, nil)
				Expect(err).NotTo(HaveOccurred())
			})
			It("errors when any secrets are passed", func() {
				secrets := serviceadapter.ManifestSecrets{"a secret": "the secret"}
				err := binder.DeleteBinding("binding-id", nil, bosh.BoshManifest{}, nil, secrets)
				Expect(err).To(MatchError(ContainSubstring("DeleteBinding received secrets when secure manifests are disabled")))
			})
		})
	})
})

func path(s string) string {
	return "((" + s + "))"
}

func defaultMap() serviceadapter.ManifestSecrets {
	return serviceadapter.ManifestSecrets{
		path(adapter.GeneratedSecretKey):       "value1",
		path(adapter.ManagedSecretKey):         "value2",
		"((instance_certificate.ca))":          "ca-val",
		"((instance_certificate.private_key))": "priv-val",
		"((instance_certificate.certificate))": "priv-val",
		"((default_secret))":                   "sec-value",
	}
}

func secretsMap(m serviceadapter.ManifestSecrets, key, value string) serviceadapter.ManifestSecrets {
	m[key] = value
	return m
}
