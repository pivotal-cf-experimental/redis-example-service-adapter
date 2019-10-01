package adapter_test

import (
	"errors"
	"io"
	"log"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
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
					{
						Jobs: []bosh.Job{
							{
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
					},
				},
			}
		})


		DescribeTable("binding",
			func(secretPath string, resolvedSecrets serviceadapter.ManifestSecrets, secretInBinding string, expectedErr error) {
				if secretPath != "" {
					properties := manifest.InstanceGroups[0].Jobs[0].Properties["redis"].(map[interface{}]interface{})
					properties["secret"] = secretPath
				}
				params := serviceadapter.CreateBindingParams{
					BindingID:          bindingID,
					DeploymentTopology: topology,
					Manifest:           manifest,
					RequestParams:      params,
					Secrets:            resolvedSecrets,
					DNSAddresses:       serviceadapter.DNSAddresses{},
				}
				binding, err := binder.CreateBinding(params)
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
				params := serviceadapter.CreateBindingParams{
					BindingID:          bindingID,
					DeploymentTopology: topology,
					Manifest:           manifest,
					RequestParams:      params,
					Secrets:            defaultMap(),
					DNSAddresses:       dnsAddresses,
				}
				binding, err := binder.CreateBinding(params)
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
			params serviceadapter.DeleteBindingParams
			logger *gbytes.Buffer
		)

		BeforeEach(func() {
			logger = gbytes.NewBuffer()
			binder = adapter.Binder{
				StderrLogger: log.New(io.MultiWriter(logger, GinkgoWriter), "delete-binding", log.LstdFlags),
			}
			params = serviceadapter.DeleteBindingParams{
				BindingID:          "binding-id",
				DeploymentTopology: nil,
				Manifest:           bosh.BoshManifest{},
				RequestParams:      nil,
				Secrets:            nil,
			}
		})

		Describe("secure manifests are enabled", func() {
			BeforeEach(func() {
				binder.Config.SecureManifestsEnabled = true
			})
			It("returns an error when it doesn't have the right credentials", func() {
				badSecretKey := "unwanted-secret"
				Expect(badSecretKey).ToNot(Equal(adapter.GeneratedSecretVariableName))
				params.Secrets = serviceadapter.ManifestSecrets{"((" + badSecretKey + "))": "not the password"}

				err := binder.DeleteBinding(params)
				Expect(err).To(MatchError("The required secret was not provided to DeleteBinding"))
			})

			It("returns an error when the provided credential is empty", func() {
				params.Secrets = serviceadapter.ManifestSecrets{"((" + adapter.GeneratedSecretVariableName + "))": ""}

				err := binder.DeleteBinding(params)
				Expect(err).To(MatchError("The incorrect secret value was provided to DeleteBinding"))
			})

			It("doesn't return an error when it has the right credentials", func() {
				params.Secrets = serviceadapter.ManifestSecrets{"((" + adapter.GeneratedSecretVariableName + "))": "bosh generated passw0rd"}

				err := binder.DeleteBinding(params)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("secure manifests are disabled", func() {
			BeforeEach(func() {
				binder.Config.SecureManifestsEnabled = false
			})
			It("does not error when no secrets are passed", func() {

				err := binder.DeleteBinding(params)
				Expect(err).NotTo(HaveOccurred())
			})
			It("errors when any secrets are passed", func() {
				params.Secrets = serviceadapter.ManifestSecrets{"a secret": "the secret"}
				err := binder.DeleteBinding(params)
				Expect(err).To(MatchError(ContainSubstring("DeleteBinding received secrets when secure manifests are disabled")))
			})
		})

		It("logs DNSAddresses", func() {
			params.DNSAddresses = map[string]string{
				"foo": "a.b.c",
			}
			err := binder.DeleteBinding(params)
			Expect(err).NotTo(HaveOccurred())
			Expect(logger).To(gbytes.Say(`{"foo":"a.b.c"}`))
		})
	})

	Describe("binding", func() {

		var (
			actualBinding    serviceadapter.Binding
			actualBindingErr error
			stderr           *gbytes.Buffer
			stderrLogger     *log.Logger
			binder           adapter.Binder
			expectedPassword = "expectedPassword"
			boshVMs          bosh.BoshVMs
			currentManifest  bosh.BoshManifest
		)

		BeforeEach(func() {
			boshVMs = bosh.BoshVMs{"redis-server": []string{"an-ip"}}
			currentManifest = bosh.BoshManifest{
				InstanceGroups: []bosh.InstanceGroup{
					{
						Jobs: []bosh.Job{
							{
								Properties: map[string]interface{}{"redis": map[interface{}]interface{}{"password": expectedPassword}},
							},
						},
					},
				},
			}
			stderr = gbytes.NewBuffer()
			stderrLogger = log.New(io.MultiWriter(stderr, GinkgoWriter), "", log.LstdFlags)
			binder = adapter.Binder{StderrLogger: stderrLogger}

		})

		JustBeforeEach(func() {
			params := serviceadapter.CreateBindingParams{
				BindingID:          "not-relevant",
				DeploymentTopology: boshVMs,
				Manifest:           currentManifest,
				RequestParams:      nil,
				Secrets:            nil,
				DNSAddresses:       nil,
			}
			actualBinding, actualBindingErr = binder.CreateBinding(params)
		})

		Context("has a password in the manifest", func() {
			It("has no error", func() {
				Expect(actualBindingErr).NotTo(HaveOccurred())
			})

			It("returns the password from the manifest", func() {
				Expect(actualBinding.Credentials["password"]).To(Equal(expectedPassword))
			})

			It("returns the host from the vms", func() {
				Expect(actualBinding.Credentials["host"]).To(Equal("an-ip"))
			})
		})

		Context("when the bosh vms don't have redis-server", func() {
			BeforeEach(func() {
				boshVMs = bosh.BoshVMs{"redis-server1": []string{"an-ip"}}
			})
			It("returns an error for the cli user", func() {
				Expect(actualBindingErr).To(HaveOccurred())
				Expect(actualBindingErr).To(MatchError(""))
			})
			It("logs an error for the operator", func() {
				Expect(stderr).To(gbytes.Say("expected redis-server instance group to have only 1 instance, got 0"))
			})
		})

		Context("when the bosh vms has a redis-server key, but it has no instances", func() {
			BeforeEach(func() {
				boshVMs = bosh.BoshVMs{"redis-server": []string{}}
			})
			It("returns an error for the cli user", func() {
				Expect(actualBindingErr).To(HaveOccurred())
				Expect(actualBindingErr).To(MatchError(""))
			})
			It("logs an error for the operator", func() {
				Expect(stderr).To(gbytes.Say("expected redis-server instance group to have only 1 instance, got 0"))
			})
		})

		Context("when there are no instance groups for Redis", func() {
			BeforeEach(func() {
				boshVMs = bosh.BoshVMs{}
			})
			It("returns an error for the cli user", func() {
				Expect(actualBindingErr).To(HaveOccurred())
				Expect(actualBindingErr).To(MatchError(""))
			})
			It("logs an error for the operator", func() {
				Expect(stderr).To(gbytes.Say("expected 1 instance group in the Redis deployment, got 0"))
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
