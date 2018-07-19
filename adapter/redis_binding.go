package adapter

import (
	"errors"
	"fmt"
	"log"
	"regexp"

	"github.com/pivotal-cf/on-demand-services-sdk/bosh"
	"github.com/pivotal-cf/on-demand-services-sdk/serviceadapter"
)

type Binder struct {
	StderrLogger *log.Logger
}

func (b Binder) CreateBinding(bindingID string, deploymentTopology bosh.BoshVMs, manifest bosh.BoshManifest, requestParams serviceadapter.RequestParameters, secrets serviceadapter.ManifestSecrets, dnsAddresses serviceadapter.DNSAddresses) (serviceadapter.Binding, error) {
	ctx := requestParams.ArbitraryContext()
	platform := requestParams.Platform()
	if len(ctx) == 0 || platform == "" || platform != "cloudfoundry" {
		b.StderrLogger.Println("Non Cloud Foundry platform (or pre OSBAPI 2.13) detected")
	}
	redisHost, err := getRedisHost(deploymentTopology)
	if err != nil {
		b.StderrLogger.Println(err.Error())
		return serviceadapter.Binding{}, errors.New("")
	}

	resolvedSecrets := make(map[string]string, len(secrets))
	if secrets != nil { // service created with latest generate-manifest
		manifestSecretPaths := []struct {
			Name     string
			Optional bool
		}{
			{Name: GeneratedSecretKey},
			{Name: ManagedSecretKey},
			{Name: "ca_cert"},
			{Name: "private_key"},
			{Name: "certificate"},
			{Name: "secret", Optional: true},
		}
		for _, field := range manifestSecretPaths {
			var ok bool
			manifestSecret := field.Name
			path, ok := redisPlanProperties(manifest)[manifestSecret].(string)
			if !ok || path == "" {
				err := fmt.Errorf("could not find path for " + manifestSecret)
				b.StderrLogger.Println(err.Error())
				if field.Optional {
					continue
				}
				return serviceadapter.Binding{}, err
			}

			matchResult, err := regexp.MatchString(`\(\([^()]+\)\)`, path)
			if err != nil {
				b.StderrLogger.Println(err.Error())
				return serviceadapter.Binding{}, err
			}

			if !matchResult {
				err := fmt.Errorf("expecting a credhub ref string with format ((xxx)), but got: %s", path)
				b.StderrLogger.Println(err.Error())
				return serviceadapter.Binding{}, err
			}

			value := secrets[path]
			if !ok || value == "" {
				err := errors.New("manifest wasn't correctly interpolated: missing value for `" + path + "`")
				b.StderrLogger.Println(err.Error())
				return serviceadapter.Binding{}, err
			}
			resolvedSecrets[path] = value
		}
	}

	var secretKey string
	if value, ok := redisPlanProperties(manifest)["secret"].(string); ok {
		secretKey = value
	}
	return serviceadapter.Binding{
		Credentials: map[string]interface{}{
			"host":                      redisHost,
			"port":                      RedisServerPort,
			"generated_secret":          resolvedSecrets[GeneratedSecretKey],
			"password":                  redisPlanProperties(manifest)["password"].(string),
			"secret":                    resolvedSecrets[secretKey],
			"odb_managed_secret":        resolvedSecrets[ManagedSecretKey],
			"dns_addresses":             dnsAddresses,
			"passed_in_secrets":         secrets,
			"expected_resolved_secrets": resolvedSecrets,
		},
	}, nil
}

func (b Binder) DeleteBinding(bindingID string, deploymentTopology bosh.BoshVMs, manifest bosh.BoshManifest, requestParams serviceadapter.RequestParameters) error {
	return nil
}

func getRedisHost(deploymentTopology bosh.BoshVMs) (string, error) {
	if len(deploymentTopology) != 1 {
		return "", fmt.Errorf("expected 1 instance group in the Redis deployment, got %d", len(deploymentTopology))
	}

	redisServerIPs := deploymentTopology["redis-server"]
	if len(redisServerIPs) != 1 {
		return "", fmt.Errorf("expected redis-server instance group to have only 1 instance, got %d", len(redisServerIPs))
	}
	return redisServerIPs[0], nil

}
