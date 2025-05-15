/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Neo4jSpec controls the behavior of the neo4j generator.
type Neo4jSpec struct {
	// Auth contains the credentials or auth configuration
	Auth Neo4jAuth `json:"auth"`
	// URI is the connection URI for the Neo4j database.
	// Example: bolt://neo4j.default.svc.cluster.local:7687
	URI string `json:"uri"`
}

type Neo4jAuth struct {
	// Basic auth credentials used to authenticate against the Neo4j instance.
	// +optional
	Basic *Neo4jBasicAuth `json:"basic,omitempty"`
	// Bearer auth token used to authenticate against the Neo4j instance.
	// +optional
	Bearer *Neo4jBearerAuth `json:"bearer,omitempty"`
}

type Neo4jBasicAuth struct {
	// A basic auth username used to authenticate against the Neo4j instance.
	Username string `json:"username"`
	// A basic auth password used to authenticate against the Neo4j instance.
	Password SecretKeySelector `json:"password"`
}

type Neo4jBearerAuth struct {
	// A bearer auth token used to authenticate against the Neo4j instance.
	Token SecretKeySelector `json:"token"`
}

type Neo4jUser struct {
	User                   string   `json:"user"`
	Roles                  []string `json:"roles"`
	PasswordChangeRequired bool     `json:"passwordChangeRequired"`
	Suspended              bool     `json:"suspended"`
	Home                   string   `json:"home"`
}

// Neo4j generates a random neo4j based on the
// configuration parameters in spec.
// You can specify the length, characterset and other attributes.
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:metadata:labels="external-secrets.io/component=controller"
// +kubebuilder:resource:scope=Namespaced,categories={external-secrets, external-secrets-generators}
type Neo4j struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec Neo4jSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// Neo4jList contains a list of ExternalSecret resources.
type Neo4jList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Neo4j `json:"items"`
}
