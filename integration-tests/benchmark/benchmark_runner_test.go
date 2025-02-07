package benchmark_test

import (
	"fmt"
	"github.com/smartcontractkit/chainlink-env/pkg/cdk8s/blockscout"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/imdario/mergo"
	"github.com/smartcontractkit/chainlink-env/environment"
	"github.com/smartcontractkit/chainlink-env/pkg/helm/chainlink"
	"github.com/smartcontractkit/chainlink-env/pkg/helm/ethereum"
	"github.com/smartcontractkit/chainlink-env/pkg/helm/remotetestrunner"
	"github.com/smartcontractkit/chainlink-testing-framework/actions"
	"github.com/smartcontractkit/chainlink-testing-framework/blockchain"
	"github.com/smartcontractkit/chainlink-testing-framework/logging"
	networks "github.com/smartcontractkit/chainlink/integration-tests"
)

func init() {
	logging.Init()
}

func getEnv(key, fallback string) string {
	if inputs, ok := os.LookupEnv("TEST_INPUTS"); ok {
		values := strings.Split(inputs, ",")
		for _, value := range values {
			if strings.Contains(value, key) {
				return strings.Split(value, "=")[1]
			}
		}
	}
	return fallback
}

var baseEnvironmentConfig = &environment.Config{
	TTL: time.Hour * 720, // 30 days,
}

var chainlinkPerformance = map[string]interface{}{
	"chainlink": map[string]interface{}{
		"resources": map[string]interface{}{
			"requests": map[string]interface{}{
				"cpu":    "1000m",
				"memory": "4Gi",
			},
			"limits": map[string]interface{}{
				"cpu":    "1000m",
				"memory": "4Gi",
			},
		},
	},
	"db": map[string]interface{}{
		"resources": map[string]interface{}{
			"requests": map[string]interface{}{
				"cpu":    "1000m",
				"memory": "1Gi",
			},
			"limits": map[string]interface{}{
				"cpu":    "1000m",
				"memory": "1Gi",
			},
		},
		"stateful": true,
		"capacity": "20Gi",
	},
}

var chainlinkSoak = map[string]interface{}{
	"chainlink": map[string]interface{}{
		"resources": map[string]interface{}{
			"requests": map[string]interface{}{
				"cpu":    "350m",
				"memory": "1Gi",
			},
			"limits": map[string]interface{}{
				"cpu":    "350m",
				"memory": "1Gi",
			},
		},
	},
	"db": map[string]interface{}{
		"resources": map[string]interface{}{
			"requests": map[string]interface{}{
				"cpu":    "250m",
				"memory": "256Mi",
			},
			"limits": map[string]interface{}{
				"cpu":    "250m",
				"memory": "256Mi",
			},
		},
		"stateful": true,
		"capacity": "20Gi",
	},
}

func TestAutomationBenchmark(t *testing.T) {
	registryToTest := getEnv("AUTOMATION_REGISTRY_TO_TEST", "registry-2-0")
	var numberOfNodes, _ = strconv.Atoi(getEnv("AUTOMATION_NUMBER_OF_NODES", "6"))
	KeeperBenchmark(t, registryToTest, numberOfNodes)
}

// Run the Keepers Benchmark test defined in ./tests/keeper_test.go
func KeeperBenchmark(t *testing.T, registryToTest string, numberOfNodes int) {
	activeEVMNetwork := networks.SelectedNetwork // Environment currently being used to run benchmark test on

	blockTime := "1"

	// Values you want each node to have the exact same of (e.g. eth_chain_id)
	staticValues := map[string]interface{}{
		"ETH_URL":      activeEVMNetwork.URLs[0],
		"ETH_HTTP_URL": activeEVMNetwork.HTTPURLs[0],
		"ETH_CHAIN_ID": strconv.Itoa(int(activeEVMNetwork.ChainID)),
	}

	keeperBenchmarkValues := map[string]interface{}{
		"MIN_INCOMING_CONFIRMATIONS":  "1",
		"KEEPER_TURN_FLAG_ENABLED":    "true",
		"CHAINLINK_DEV":               "false",
		"P2P_NETWORKING_STACK":        "V2",
		"P2PV2_LISTEN_ADDRESSES":      "0.0.0.0:6690",
		"P2PV2_ANNOUNCE_ADDRESSES":    "0.0.0.0:6690",
		"FEATURE_OFFCHAIN_REPORTING2": "true",
		"FEATURE_OFFCHAIN_REPORTING":  "",
		"FEATURE_LOG_POLLER":          "true",
		"P2P_LISTEN_IP":               "",
		"P2P_LISTEN_PORT":             "",
	}

	network := "simulated"

	if registryToTest == "registry-2-0" {
		numberOfNodes = numberOfNodes + 1
		blockTime = "12"
		keeperBenchmarkValues["KEEPER_TURN_FLAG_ENABLED"] = "false"
	}

	// List of distinct Chainlink nodes to launch, and their distinct values (blank interface for none)
	var dynamicValues []map[string]interface{}
	for i := 0; i < numberOfNodes; i++ {
		dynamicValues = append(dynamicValues, map[string]interface{}{"": ""})
	}

	if !activeEVMNetwork.Simulated {
		keeperBenchmarkValues["KEEPER_REGISTRY_SYNC_INTERVAL"] = ""
		keeperBenchmarkValues["MIN_INCOMING_CONFIRMATIONS"] = ""
		keeperBenchmarkValues["ETH_MAX_IN_FLIGHT_TRANSACTIONS"] = ""
		keeperBenchmarkValues["ETH_MAX_QUEUED_TRANSACTIONS"] = ""
		keeperBenchmarkValues["ETH_GAS_BUMP_TX_DEPTH"] = ""
		network = strings.ReplaceAll(strings.ToLower(activeEVMNetwork.Name), " ", "-")
	}
	testType := strings.ToLower(getEnv("TEST_TYPE", "benchmark"))

	baseEnvironmentConfig.NamespacePrefix = fmt.Sprintf(
		"automation-%s-%s-%s",
		testType, network, registryToTest,
	)

	testEnvironment := environment.New(baseEnvironmentConfig)
	testTag := "@" + network + "-" + registryToTest
	mergo.Merge(&staticValues, &keeperBenchmarkValues)
	addSeparateChainlinkDeployments(testEnvironment, testType, staticValues, dynamicValues)
	benchmarkTestHelper(t, testTag+" @benchmark-keeper", testEnvironment, activeEVMNetwork, blockTime)
}

// adds distinct Chainlink deployments to the test environment, using staticVals on all of them, while distributing
// a single dynamicVal to each Chainlink deployment
func addSeparateChainlinkDeployments(
	testEnvironment *environment.Environment,
	testType string,
	staticValues map[string]interface{},
	dynamicValueList []map[string]interface{},
) {
	for index, dynamicValues := range dynamicValueList {
		envVals := map[string]interface{}{}
		for key, value := range staticValues {
			envVals[key] = value
		}
		for key, value := range dynamicValues {
			envVals[key] = value
		}
		chartValues := map[string]interface{}{
			"env": envVals,
		}
		chartResources := chainlinkPerformance
		if testType == "soak" {
			chartResources = chainlinkSoak
		}
		mergo.Merge(&chartValues, &chartResources)
		testEnvironment.AddHelm(chainlink.NewVersioned(index, "0.0.11", chartValues))
	}
}

// builds tests, launches environment, and triggers the benchmark test to run
func benchmarkTestHelper(
	t *testing.T,
	testTag string,
	testEnvironment *environment.Environment,
	activeEVMNetwork *blockchain.EVMNetwork,
	blockTime string,
) {
	remoteRunnerValues := map[string]interface{}{
		"focus":                 testTag,
		"env_namespace":         testEnvironment.Cfg.Namespace,
		"test_dir":              "./integration-tests/benchmark/tests",
		"test_log_level":        "debug",
		"grafana_dashboard_url": getEnv("GRAFANA_DASHBOARD_URL", ""),
		"TEST_INPUTS":           os.Getenv("TEST_INPUTS"),
		"SELECTED_NETWORKS":     os.Getenv("SELECTED_NETWORKS"),
	}
	// Set evm network connection for remote runner
	for key, value := range activeEVMNetwork.ToMap() {
		remoteRunnerValues[key] = value
	}
	remoteRunnerWrapper := map[string]interface{}{
		"remote_test_runner": remoteRunnerValues,
	}

	if activeEVMNetwork.Simulated {
		testEnvironment.
			AddChart(blockscout.New(&blockscout.Props{
				Name:    "geth-blockscout",
				WsURL:   activeEVMNetwork.URL,
				HttpURL: activeEVMNetwork.HTTPURLs[0]}))
	}

	err := testEnvironment.
		AddHelm(remotetestrunner.New(remoteRunnerWrapper)).
		AddHelm(ethereum.New(&ethereum.Props{
			NetworkName: activeEVMNetwork.Name,
			Simulated:   activeEVMNetwork.Simulated,
			WsURLs:      activeEVMNetwork.URLs,
			Values: map[string]interface{}{
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{
						"cpu":    "4000m",
						"memory": "4Gi",
					},
					"limits": map[string]interface{}{
						"cpu":    "4000m",
						"memory": "4Gi",
					},
				},
				"geth": map[string]interface{}{
					"blocktime": blockTime,
				},
			},
		})).
		Run()
	require.NoError(t, err, "Error launching test environment")
	err = actions.TriggerRemoteTest("../../", testEnvironment)
	require.NoError(t, err, "Error activating remote test")
}
