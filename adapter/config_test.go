package adapter_test

import (
	"io"
	"log"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/pivotal-cf-experimental/redis-example-service-adapter/adapter"
)

var _ = Describe("Config", func() {
	var (
		stderr       = gbytes.NewBuffer()
		stderrLogger *log.Logger
	)

	BeforeEach(func() {
		stderrLogger = log.New(io.MultiWriter(stderr, GinkgoWriter), "create-binding", log.LstdFlags)
	})

	It("can load config from file", func() {
		configFilePath := getFixturePath("binding-config-manifest-secrets-enabled.yml")
		config, err := adapter.LoadConfig(configFilePath, stderrLogger)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.SecureManifestsEnabled).To(BeTrue())
	})

	It("can load config from file with manifest secrets disabled", func() {
		configFilePath := getFixturePath("binding-config-manifest-secrets-disabled.yml")
		config, err := adapter.LoadConfig(configFilePath, stderrLogger)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.SecureManifestsEnabled).To(BeFalse())
	})

	It("errors when the config file does not exist", func() {
		configFilePath := getFixturePath("does-not-exist.yml")
		_, err := adapter.LoadConfig(configFilePath, stderrLogger)
		Expect(err).To(MatchError(ContainSubstring("Error, could not read config file")))
		Expect(stderr).To(gbytes.Say("Error, could not read config file"))
	})

	It("errors when the config file is invalid", func() {
		configFilePath := getFixturePath("binding-config-invalid.yml")
		_, err := adapter.LoadConfig(configFilePath, stderrLogger)
		Expect(err).To(MatchError(ContainSubstring("Error, could not parse config YAML")))
		Expect(stderr).To(gbytes.Say("Error, could not parse config YAML"))
	})
})
