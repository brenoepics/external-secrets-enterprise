// Copyright External Secrets Inc. All Rights Reserved
package neo4j

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"testing"

	genv1alpha1 "github.com/external-secrets/external-secrets/apis/generators/v1alpha1"
	neo4jSDK "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcgNeo4j "github.com/testcontainers/testcontainers-go/modules/neo4j"
	corev1 "k8s.io/api/core/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// mockClient implements the client.Client interface for testing.
type generatorMockClient struct {
	client.Client
	userPassword []byte
}

const (
	testPass = "strongpassword"
)

// Get implements client.Client.
func (m generatorMockClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {

	if key.Name == "testpass" {
		obj.(*corev1.Secret).Data = map[string][]byte{
			"password": []byte(testPass),
		}
	} else if key.Name == "userpass" {
		obj.(*corev1.Secret).Data = map[string][]byte{
			"password": m.userPassword,
		}
	}
	return nil
}

func setupNeo4jContainer(ctx context.Context, t *testing.T) (testcontainers.Container, string) {
	t.Helper()

	// Start Neo4j container
	neo4jContainer, err := tcgNeo4j.Run(ctx,
		"neo4j:2025.04.0",
		tcgNeo4j.WithAdminPassword(testPass),
	)
	require.NoError(t, err)

	// Automatically clean up the container when the test ends
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(neo4jContainer); err != nil {
			t.Logf("failed to terminate container: %s", err)
		}
	})

	port, err := neo4jContainer.MappedPort(ctx, "7687")
	require.NoError(t, err)

	host, err := neo4jContainer.Host(ctx)
	require.NoError(t, err)

	uri := fmt.Sprintf("bolt://%s:%s", host, port.Port())
	return neo4jContainer, uri
}

func TestNeo4jGeneratorIntegration(t *testing.T) {
	ctx := context.Background()
	neo4jContainer, uri := setupNeo4jContainer(ctx, t)

	require.True(t, neo4jContainer.IsRunning())

	// build generator input
	username := "generated_user"
	spec := &genv1alpha1.Neo4j{
		Spec: genv1alpha1.Neo4jSpec{
			Auth: genv1alpha1.Neo4jAuth{
				URI: uri,
				Basic: &genv1alpha1.Neo4jBasicAuth{
					Username: "neo4j",
					Password: genv1alpha1.SecretKeySelector{
						Name: "testpass",
						Key:  "password",
					},
				},
			},
			User: &genv1alpha1.Neo4jUser{
				User:     username,
				Roles:    []string{"reader"},
				Provider: genv1alpha1.Neo4jAuthProviderNative,
			},
		},
	}
	specJSON, _ := yaml.Marshal(spec)

	// call Generate()
	gen := &Generator{}
	kube := generatorMockClient{}
	result, rawStatus, err := gen.Generate(ctx, &apiextensions.JSON{Raw: specJSON}, kube, "default")

	require.NoError(t, err)
	require.Contains(t, result, "user")
	assert.Equal(t, result["user"], []byte(username))
	require.Contains(t, result, "password")

	status, err := parseStatus(rawStatus.Raw)
	require.NoError(t, err)

	assert.Equal(t, status.User, username)

	// Verify that the user was created in the Neo4j database
	kube.userPassword = result["password"]

	driver, err := newDriver(ctx, &genv1alpha1.Neo4jAuth{
		URI: uri,
		Basic: &genv1alpha1.Neo4jBasicAuth{
			Username: username,
			Password: genv1alpha1.SecretKeySelector{
				Name: "userpass",
				Key:  "password",
			},
		},
	}, kube, "default")
	require.NoError(t, err)
	defer func() {
		err := driver.Close(ctx)
		if err != nil {
			log.Printf("failed to close driver: %v", err)
		}
	}()
	err = driver.VerifyConnectivity(ctx)
	require.NoError(t, err)
}

func TestNeo4jGeneratorWithRandomSufix(t *testing.T) {
	ctx := context.Background()
	neo4jContainer, uri := setupNeo4jContainer(ctx, t)

	require.True(t, neo4jContainer.IsRunning())

	// build generator input
	spec := &genv1alpha1.Neo4j{
		Spec: genv1alpha1.Neo4jSpec{
			Auth: genv1alpha1.Neo4jAuth{
				URI: uri,
				Basic: &genv1alpha1.Neo4jBasicAuth{
					Username: "neo4j",
					Password: genv1alpha1.SecretKeySelector{
						Name: "testpass",
						Key:  "password",
					},
				},
			},
			User: &genv1alpha1.Neo4jUser{
				User:        "generated_user",
				RandomSufix: true,
				Roles:       []string{"reader"},
				Provider:    genv1alpha1.Neo4jAuthProviderNative,
			},
		},
	}
	specJSON, _ := yaml.Marshal(spec)

	// call Generate()
	gen := &Generator{}
	kube := generatorMockClient{}
	result, rawStatus, err := gen.Generate(ctx, &apiextensions.JSON{Raw: specJSON}, kube, "default")

	require.NoError(t, err)
	require.Contains(t, result, "user")

	userRegex := regexp.MustCompile(`generated_user\d{4}`)
	assert.Regexp(t, userRegex, string(result["user"]))
	require.Contains(t, result, "password")

	status, err := parseStatus(rawStatus.Raw)
	require.NoError(t, err)

	assert.Regexp(t, userRegex, status.User)
}

func TestNeo4jCleanup(t *testing.T) {
	ctx := context.Background()
	neo4jContainer, uri := setupNeo4jContainer(ctx, t)

	require.True(t, neo4jContainer.IsRunning())

	// build generator input
	username := "generated_user"
	spec := &genv1alpha1.Neo4j{
		Spec: genv1alpha1.Neo4jSpec{
			Auth: genv1alpha1.Neo4jAuth{
				URI: uri,
				Basic: &genv1alpha1.Neo4jBasicAuth{
					Username: "neo4j",
					Password: genv1alpha1.SecretKeySelector{
						Name: "testpass",
						Key:  "password",
					},
				},
			},
			User: &genv1alpha1.Neo4jUser{
				User:     username,
				Roles:    []string{"reader"},
				Provider: genv1alpha1.Neo4jAuthProviderNative,
			},
		},
	}
	specJSON, _ := yaml.Marshal(spec)

	// call Generate()
	gen := &Generator{}
	kube := generatorMockClient{}
	result, rawStatus, err := gen.Generate(ctx, &apiextensions.JSON{Raw: specJSON}, kube, "default")

	require.NoError(t, err)

	_, err = parseStatus(rawStatus.Raw)
	require.NoError(t, err)

	// Verify that the user was created in the Neo4j database
	kube.userPassword = result["password"]

	userAuth := &genv1alpha1.Neo4jBasicAuth{
		Username: username,
		Password: genv1alpha1.SecretKeySelector{
			Name: "userpass",
			Key:  "password",
		},
	}

	driver, err := newDriver(ctx, &genv1alpha1.Neo4jAuth{
		URI:   uri,
		Basic: userAuth,
	}, kube, "default")
	require.NoError(t, err)
	err = driver.VerifyConnectivity(ctx)
	require.NoError(t, err)
	err = driver.Close(ctx)
	require.NoError(t, err)

	// Cleanup
	err = gen.Cleanup(ctx, &apiextensions.JSON{Raw: specJSON}, rawStatus, kube, "default")
	require.NoError(t, err)

	driver, err = newDriver(ctx, &genv1alpha1.Neo4jAuth{
		URI:   uri,
		Basic: userAuth,
	}, kube, "default")
	require.NoError(t, err)
	defer func() {
		err := driver.Close(ctx)
		if err != nil {
			log.Printf("failed to close driver: %v", err)
		}
	}()
	err = driver.VerifyConnectivity(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Neo4jError: Neo.ClientError.Security.Unauthorized")
}

func TestNeo4jCleanupAfterUserDBManipulation(t *testing.T) {
	ctx := context.Background()
	neo4jContainer, uri := setupNeo4jContainer(ctx, t)

	require.True(t, neo4jContainer.IsRunning())

	// build generator input
	username := "generated_user"
	spec := &genv1alpha1.Neo4j{
		Spec: genv1alpha1.Neo4jSpec{
			Auth: genv1alpha1.Neo4jAuth{
				URI: uri,
				Basic: &genv1alpha1.Neo4jBasicAuth{
					Username: "neo4j",
					Password: genv1alpha1.SecretKeySelector{
						Name: "testpass",
						Key:  "password",
					},
				},
			},
			User: &genv1alpha1.Neo4jUser{
				User:     username,
				Roles:    []string{"reader"},
				Provider: genv1alpha1.Neo4jAuthProviderNative,
			},
		},
	}
	specJSON, _ := yaml.Marshal(spec)

	// call Generate()
	gen := &Generator{}
	kube := generatorMockClient{}
	result, rawStatus, err := gen.Generate(ctx, &apiextensions.JSON{Raw: specJSON}, kube, "default")

	require.NoError(t, err)

	_, err = parseStatus(rawStatus.Raw)
	require.NoError(t, err)

	// Verify that the user was created in the Neo4j database
	kube.userPassword = result["password"]

	userAuth := &genv1alpha1.Neo4jBasicAuth{
		Username: username,
		Password: genv1alpha1.SecretKeySelector{
			Name: "userpass",
			Key:  "password",
		},
	}

	driver, err := newDriver(ctx, &genv1alpha1.Neo4jAuth{
		URI:   uri,
		Basic: userAuth,
	}, kube, "default")
	require.NoError(t, err)
	err = driver.VerifyConnectivity(ctx)
	require.NoError(t, err)

	resultQuery, err := neo4jSDK.ExecuteQuery(
		ctx, driver,
		`CREATE (:Test {message: "Hello from new user"})`,
		map[string]any{},
		neo4jSDK.EagerResultTransformer,
		neo4jSDK.ExecuteQueryWithDatabase(spec.Spec.Database),
	)
	require.NoError(t, err)
	require.NotNil(t, resultQuery)
	t.Logf("result: %v", resultQuery)

	err = driver.Close(ctx)
	require.NoError(t, err)

	// Cleanup
	err = gen.Cleanup(ctx, &apiextensions.JSON{Raw: specJSON}, rawStatus, kube, "default")
	require.NoError(t, err)

	driver, err = newDriver(ctx, &genv1alpha1.Neo4jAuth{
		URI:   uri,
		Basic: spec.Spec.Auth.Basic,
	}, kube, "default")
	require.NoError(t, err)
	defer func() {
		err := driver.Close(ctx)
		if err != nil {
			log.Printf("failed to close driver: %v", err)
		}
	}()
	err = driver.VerifyConnectivity(ctx)
	require.NoError(t, err)

	resultQuery, err = neo4jSDK.ExecuteQuery(
		ctx, driver,
		`MATCH (n:Test {message: "Hello from new user"}) RETURN n`,
		map[string]any{},
		neo4jSDK.EagerResultTransformer,
		neo4jSDK.ExecuteQueryWithDatabase(spec.Spec.Database),
	)
	require.NoError(t, err)
	require.NotNil(t, resultQuery)

	// Check if the node was not deleted
	resultMap := resultQuery.Records[0].AsMap()
	require.Contains(t, resultMap, "n")
	assert.NotNil(t, resultMap["n"])
	assert.NotNil(t, resultMap["n"].(neo4jSDK.Node).GetProperties()["message"])
	assert.Equal(t, resultMap["n"].(neo4jSDK.Node).GetProperties()["message"], "Hello from new user")
}
