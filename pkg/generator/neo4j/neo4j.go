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

	driver, err := newDriver(ctx, res, kube, namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to create driver: %w", err)
	}
	defer driver.Close(ctx)

	err = driver.VerifyConnectivity(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to verify connectivity: %w", err)
	}

	state, err := getUser(ctx, driver, res)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to get user: %w", err)
	}

	var rawState []byte
	if state != nil {
		rawState, err = json.Marshal(state)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to marshal state: %w", err)
		}
	}

	user, err := createOrReplaceUser(ctx, driver, res)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to create or replace user: %w", err)
	}

	return user, &apiextensions.JSON{Raw: rawState}, nil
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

func getUser(ctx context.Context, driver neo4j.DriverWithContext, spec *genv1alpha1.Neo4j) (*genv1alpha1.Neo4jUser, error) {
	result, err := neo4j.ExecuteQuery(ctx, driver,
		"SHOW USERS WITH AUTH", map[string]any{},
		neo4j.EagerResultTransformer,
		neo4j.ExecuteQueryWithDatabase("neo4j"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	var user *genv1alpha1.Neo4jUser
	// Loop through results and do something with them
	for _, record := range result.Records {
		recordMap := record.AsMap()
		name, ok := recordMap["name"].(string)
		if !ok || name != spec.Spec.User.User {
			continue
		}

		user, err = ParseNeo4jUser(*record)
		if err != nil {
			return nil, fmt.Errorf("failed to parse user: %w", err)
		}
	}

	if user == nil {
		return nil, fmt.Errorf("user not found: %s", spec.Spec.User.User)
	}

	return user, nil
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
		"name":     []byte(spec.Spec.User.User),
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

func ParseNeo4jUser(record neo4j.Record) (*genv1alpha1.Neo4jUser, error) {
	m := record.AsMap()

	// Required fields
	name, ok := m["name"].(string)
	if !ok {
		return nil, fmt.Errorf("field 'name' is missing or not a string")
	}

	providerStr, ok := m["provider"].(string)
	if !ok {
		return nil, fmt.Errorf("field 'provider' is missing or not a string")
	}

	// Optional fields
	var roles []string
	if rawRoles, ok := m["roles"].([]interface{}); ok {
		roles = make([]string, len(rawRoles))
		for i, r := range rawRoles {
			if str, ok := r.(string); ok {
				roles[i] = str
			}
		}
	}

	var homePtr *string
	if home, ok := m["home"].(string); ok {
		homePtr = &home
	}

	var suspendedPtr *bool
	if suspended, ok := m["suspended"].(bool); ok {
		suspendedPtr = &suspended
	}
	passwordChangeRequired, _ := m["passwordChangeRequired"].(bool)

	var auth map[string]interface{}
	if rawAuth, ok := m["auth"].(map[string]interface{}); ok {
		auth = rawAuth
	}

	return &genv1alpha1.Neo4jUser{
		User:                   name,
		Roles:                  roles,
		Suspended:              suspendedPtr,
		PasswordChangeRequired: passwordChangeRequired,
		Home:                   homePtr,
		Provider:               genv1alpha1.Neo4jAuthProvider(providerStr),
		Auth:                   auth,
	}, nil
}

func parseSpec(data []byte) (*genv1alpha1.Neo4j, error) {
	var spec genv1alpha1.Neo4j
	err := yaml.Unmarshal(data, &spec)
	return &spec, err
}

func init() {
	genv1alpha1.Register(genv1alpha1.Neo4jKind, &Generator{})
}
