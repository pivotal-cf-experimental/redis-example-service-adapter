package adapter

import (
	"fmt"
	"log"
	"regexp"

	"github.com/pivotal-cf/on-demand-services-sdk/bosh"
	"github.com/pivotal-cf/on-demand-services-sdk/serviceadapter"
	"github.com/pkg/errors"
)

type Binder struct {
	StderrLogger *log.Logger
	Config       Config
}

func (b Binder) CreateBinding(params serviceadapter.CreateBindingParams) (serviceadapter.Binding, error) {
	ctx := params.RequestParams.ArbitraryContext()
	platform := params.RequestParams.Platform()
	if len(ctx) == 0 || platform == "" || platform != "cloudfoundry" {
		b.StderrLogger.Println("Non Cloud Foundry platform (or pre OSBAPI 2.13) detected")
	}
	redisHost, err := getRedisHost(params.DeploymentTopology)
	if err != nil {
		b.StderrLogger.Println(err.Error())
		return serviceadapter.Binding{}, errors.New("")
	}

	resolvedSecrets := make(map[string]string, len(params.Secrets))
	if params.Secrets != nil { // service created with latest generate-manifest
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
			path, ok := redisPlanProperties(params.Manifest)[manifestSecret].(string) // ((/odb/....))
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

			value, ok := params.Secrets[path]
			if !ok || value == "" {
				err := errors.New("manifest wasn't correctly interpolated: missing value for `" + path + "`")
				b.StderrLogger.Println(err.Error())
				return serviceadapter.Binding{}, err
			}
			resolvedSecrets[field.Name] = value
		}
	}

	return serviceadapter.Binding{
		Credentials: map[string]interface{}{
			"host":                      redisHost,
			"port":                      RedisServerPort,
			"generated_secret":          resolvedSecrets[GeneratedSecretKey],
			"password":                  redisPlanProperties(params.Manifest)["password"].(string),
			"secret":                    resolvedSecrets["secret"],
			"odb_managed_secret":        resolvedSecrets[ManagedSecretKey],
			"dns_addresses":             params.DNSAddresses,
			"passed_in_secrets":         params.Secrets,
			"expected_resolved_secrets": resolvedSecrets,
		},
	}, nil
}

func (b Binder) DeleteBinding(params serviceadapter.DeleteBindingParams) error {
	b.StderrLogger.Printf("DNS addresses: %#v", params.DNSAddresses)

	if !b.Config.SecureManifestsEnabled {
		if len(params.Secrets) != 0 {
			return errors.New("DeleteBinding received secrets when secure manifests are disabled")
		}
		return nil
	}

	actualSecretValue, ok := params.Secrets["(("+GeneratedSecretVariableName+"))"]
	if !ok {
		return errors.New("The required secret was not provided to DeleteBinding")
	}

	secretIsValid := simulatedLoginToRedisSucceeds(actualSecretValue)
	if !secretIsValid {
		return errors.New("The incorrect secret value was provided to DeleteBinding")
	}
	return nil
}

func simulatedLoginToRedisSucceeds(password string) bool {
	return len(password) > 0
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
