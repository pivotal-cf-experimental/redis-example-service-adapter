package adapter

import (
	"errors"
	"fmt"
	"log"

	"github.com/pivotal-cf/on-demand-services-sdk/bosh"
	"github.com/pivotal-cf/on-demand-services-sdk/serviceadapter"
)

type Binder struct {
	StderrLogger *log.Logger
}

func (b Binder) CreateBinding(bindingID string, deploymentTopology bosh.BoshVMs, manifest bosh.BoshManifest, requestParams serviceadapter.RequestParameters, secrets serviceadapter.ManifestSecrets) (serviceadapter.Binding, error) {
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

	var secretFromConfigStore string
	if redisPlanProperties(manifest)["secret"] != nil {
		pathWithParens, ok := redisPlanProperties(manifest)["secret"].(string)
		if !ok {
			err := errors.New("secret in manifest was not a string. expecting a credhub ref string")
			b.StderrLogger.Println(err.Error())
			return serviceadapter.Binding{}, err
		}
		path := pathWithParens[2 : len(pathWithParens)-2]
		secretFromConfigStore = secrets[path]
	}
	return serviceadapter.Binding{
		Credentials: map[string]interface{}{
			"host":     redisHost,
			"port":     RedisServerPort,
			"password": redisPlanProperties(manifest)["password"].(string),
			"secret":   secretFromConfigStore,
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
