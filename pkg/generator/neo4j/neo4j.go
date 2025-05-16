// Copyright External Secrets Inc. All Rights Reserved

package neo4j

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	genv1alpha1 "github.com/external-secrets/external-secrets/apis/generators/v1alpha1"
	esmeta "github.com/external-secrets/external-secrets/apis/meta/v1"
	"github.com/external-secrets/external-secrets/pkg/generator/password"
	"github.com/external-secrets/external-secrets/pkg/utils/resolvers"
)

type Generator struct{}

func (g *Generator) Generate(ctx context.Context, jsonSpec *apiextensions.JSON, kube client.Client, namespace string) (map[string][]byte, genv1alpha1.GeneratorProviderState, error) {
	res, err := parseSpec(jsonSpec.Raw)
	if err != nil {
		return nil, nil, err
	}

	driver, err := newDriver(ctx, &res.Spec.Auth, kube, namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to create driver: %w", err)
	}
	defer func() {
		err := driver.Close(ctx)
		if err != nil {
			fmt.Printf("failed to close driver: %v", err)
		}
	}()

	err = driver.VerifyConnectivity(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to verify connectivity: %w", err)
	}

	user, err := createOrReplaceUser(ctx, driver, res)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to create or replace user: %w", err)
	}

	rawState, err := json.Marshal(&genv1alpha1.Neo4jUserState{
		User: res.Spec.User.User,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("unable to marshal state: %w", err)
	}

	return user, &apiextensions.JSON{Raw: rawState}, nil
}

func (g *Generator) Cleanup(ctx context.Context, jsonSpec *apiextensions.JSON, previousStatus genv1alpha1.GeneratorProviderState, kclient client.Client, namespace string) error {
	if previousStatus == nil {
		return fmt.Errorf("missing previous status")
	}
	status, err := parseStatus(previousStatus.Raw)
	if err != nil {
		return err
	}
	res, err := parseSpec(jsonSpec.Raw)
	if err != nil {
		return err
	}
	driver, err := newDriver(ctx, &res.Spec.Auth, kclient, namespace)
	if err != nil {
		return err
	}
	defer func() {
		err := driver.Close(ctx)
		if err != nil {
			fmt.Printf("failed to close driver: %v", err)
		}
	}()

	err = driver.VerifyConnectivity(ctx)
	if err != nil {
		return fmt.Errorf("unable to verify connectivity: %w", err)
	}

	query := fmt.Sprintf("DROP USER %s IF EXISTS", status.User)
	_, err = neo4j.ExecuteQuery(ctx, driver, query, nil, neo4j.EagerResultTransformer)
	if err != nil {
		return fmt.Errorf("failed to drop user: %w", err)
	}

	return nil
}

func newDriver(ctx context.Context, auth *genv1alpha1.Neo4jAuth, kclient client.Client, ns string) (neo4j.DriverWithContext, error) {
	dbUri := auth.URI
	var authToken neo4j.AuthToken
	if auth.Bearer != nil {
		bearerToken, err := resolvers.SecretKeyRef(ctx, kclient, resolvers.EmptyStoreKind, ns, &esmeta.SecretKeySelector{
			Namespace: &ns,
			Name:      auth.Bearer.Token.Name,
			Key:       auth.Bearer.Token.Key,
		})
		if err != nil {
			return nil, err
		}
		authToken = neo4j.BearerAuth(bearerToken)
	} else if auth.Basic != nil {
		dbUser := auth.Basic.Username
		dbPassword, err := resolvers.SecretKeyRef(ctx, kclient, resolvers.EmptyStoreKind, ns, &esmeta.SecretKeySelector{
			Namespace: &ns,
			Name:      auth.Basic.Password.Name,
			Key:       auth.Basic.Password.Key,
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

func createOrReplaceUser(ctx context.Context, driver neo4j.DriverWithContext, spec *genv1alpha1.Neo4j) (map[string][]byte, error) {
	var query strings.Builder
	query.WriteString(fmt.Sprintf("CREATE OR REPLACE USER %s\n", spec.Spec.User.User))

	if spec.Spec.User.Suspended != nil {
		if *spec.Spec.User.Suspended {
			query.WriteString("SET STATUS SUSPENDED\n")
		} else {
			query.WriteString("SET STATUS ACTIVE\n")
		}
	}

	if spec.Spec.User.Home != nil {
		query.WriteString(fmt.Sprintf("SET HOME DATABASE %s\n", *spec.Spec.User.Home))
	}

	query.WriteString(fmt.Sprintf("SET AUTH '%s' {\n", spec.Spec.User.Provider))

	if spec.Spec.User.Provider == genv1alpha1.Neo4jAuthProviderNative {
		pass, err := generatePassword(genv1alpha1.PasswordSpec{})
		if err != nil {
			return nil, fmt.Errorf("failed to generate password: %w", err)
		}
		query.WriteString(fmt.Sprintf("\tSET PASSWORD '%s'\n", string(pass)))

		if spec.Spec.User.PasswordChangeRequired {
			query.WriteString("\tSET PASSWORD CHANGE REQUIRED\n")
		} else {
			query.WriteString("\tSET PASSWORD CHANGE NOT REQUIRED\n")
		}
	}

	_, err := neo4j.ExecuteQuery(ctx, driver,
		query.String(), map[string]any{},
		neo4j.EagerResultTransformer,
		neo4j.ExecuteQueryWithDatabase("neo4j"),
	)
	if err != nil {
		return nil, err
	}

	return map[string][]byte{
		"user":     []byte(spec.Spec.User.User),
		"passowrd": []byte("abcd1234"),
	}, nil
}

func generatePassword(
	passSpec genv1alpha1.PasswordSpec,
) ([]byte, error) {
	gen := password.Generator{}
	rawPassSpec, err := yaml.Marshal(passSpec)
	if err != nil {
		return nil, err
	}
	passMap, _, err := gen.Generate(context.TODO(), &apiextensions.JSON{Raw: rawPassSpec}, nil, "")

	if err != nil {
		return nil, err
	}

	pass, ok := passMap["password"]
	if !ok {
		return nil, fmt.Errorf("password not found in generated map")
	}
	return pass, nil
}

func parseSpec(data []byte) (*genv1alpha1.Neo4j, error) {
	var spec genv1alpha1.Neo4j
	err := yaml.Unmarshal(data, &spec)
	return &spec, err
}

func parseStatus(data []byte) (*genv1alpha1.Neo4jUserState, error) {
	var state genv1alpha1.Neo4jUserState
	err := json.Unmarshal(data, &state)
	if err != nil {
		return nil, err
	}
	return &state, err
}

func init() {
	genv1alpha1.Register(genv1alpha1.Neo4jKind, &Generator{})
}
