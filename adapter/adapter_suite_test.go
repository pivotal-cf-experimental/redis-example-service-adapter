package adapter_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func TestRedismanifest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Redis Service Adapter Suite")
}
