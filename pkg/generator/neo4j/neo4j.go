package neo4j

import (
	"context"
	"fmt"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	genv1alpha1 "github.com/external-secrets/external-secrets/apis/generators/v1alpha1"
	esmeta "github.com/external-secrets/external-secrets/apis/meta/v1"
	"github.com/external-secrets/external-secrets/pkg/utils/resolvers"
)

type Generator struct{}

func (g *Generator) Generate(ctx context.Context, jsonSpec *apiextensions.JSON, kube client.Client, namespace string) (map[string][]byte, genv1alpha1.GeneratorProviderState, error) {
	res, err := parseSpec(jsonSpec.Raw)
	if err != nil {
		return nil, nil, err
	}

	driver, err := newDriver(ctx, res, kube, namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to create driver: %w", err)
	}
	defer driver.Close(ctx)

	err = driver.VerifyConnectivity(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to verify connectivity: %w", err)
	}

	return nil, nil, fmt.Errorf("not implemented")
}

func (g *Generator) Cleanup(ctx context.Context, jsonSpec *apiextensions.JSON, state genv1alpha1.GeneratorProviderState, kclient client.Client, namespace string) error {
	return nil
}

func newDriver(ctx context.Context, spec *genv1alpha1.Neo4j, kclient client.Client, ns string) (neo4j.DriverWithContext, error) {
	// URI examples: "neo4j://localhost", "neo4j+s://xxx.databases.neo4j.io"
	dbUri := spec.Spec.URI
	var authToken neo4j.AuthToken
	if spec.Spec.Auth.Bearer != nil {
		bearerToken, err := resolvers.SecretKeyRef(ctx, kclient, resolvers.EmptyStoreKind, ns, &esmeta.SecretKeySelector{
			Namespace: &ns,
			Name:      spec.Spec.Auth.Bearer.Token.Name,
			Key:       spec.Spec.Auth.Bearer.Token.Key,
		})
		if err != nil {
			return nil, err
		}
		authToken = neo4j.BearerAuth(bearerToken)
	} else if spec.Spec.Auth.Basic != nil {
		dbUser := spec.Spec.Auth.Basic.Username
		dbPassword, err := resolvers.SecretKeyRef(ctx, kclient, resolvers.EmptyStoreKind, ns, &esmeta.SecretKeySelector{
			Namespace: &ns,
			Name:      spec.Spec.Auth.Basic.Password.Name,
			Key:       spec.Spec.Auth.Basic.Password.Key,
		})
		if err != nil {
			return nil, err
		}
		authToken = neo4j.BasicAuth(dbUser, dbPassword, "")
	}

	return neo4j.NewDriverWithContext(
		dbUri,
		authToken,
	)
}

func getCurrentUser(ctx context.Context, spec *genv1alpha1.Neo4j, kclient client.Client, ns string) (genv1alpha1.Neo4jUser, error) {
	return genv1alpha1.Neo4jUser{}, nil
}

func parseSpec(data []byte) (*genv1alpha1.Neo4j, error) {
	var spec genv1alpha1.Neo4j
	err := yaml.Unmarshal(data, &spec)
	return &spec, err
}

func init() {
	genv1alpha1.Register(genv1alpha1.Neo4jKind, &Generator{})
}
