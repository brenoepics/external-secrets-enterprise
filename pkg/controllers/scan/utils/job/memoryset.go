package job

import (
	"crypto/sha512"
	"encoding/hex"
	"sync"

	"github.com/external-secrets/external-secrets/apis/scan/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type MemorySet struct {
	mu          sync.RWMutex
	entries     map[v1alpha1.SecretInStoreRef]string
	valueToKeys map[string][]v1alpha1.SecretInStoreRef
}

func NewMemorySet() *MemorySet {
	return &MemorySet{
		entries:     make(map[v1alpha1.SecretInStoreRef]string),
		valueToKeys: make(map[string][]v1alpha1.SecretInStoreRef),
		mu:          sync.RWMutex{},
	}
}

func (ms *MemorySet) Add(secret v1alpha1.SecretInStoreRef, value []byte) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	h := hash(value)
	ms.entries[secret] = h
	ms.valueToKeys[h] = append(ms.valueToKeys[h], secret)
}

func hash(value []byte) string {
	hash := sha512.Sum512(value)
	return hex.EncodeToString(hash[:])
}

// GetDuplicates now just scans the valueToKeys map to find values with more than one Entry.

func (ms *MemorySet) GetDuplicates() []v1alpha1.Finding {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var findings []v1alpha1.Finding
	for hash, keys := range ms.valueToKeys {
		if len(keys) > 1 {
			finding := v1alpha1.Finding{
				ObjectMeta: metav1.ObjectMeta{
					Name: hash,
				},
				Spec: v1alpha1.FindingSpec{
					Hash: hash,
				},
			}
			for _, key := range keys {
				finding.Status.Locations = append(finding.Status.Locations, key)
			}
			findings = append(findings, finding)
		}
	}
	return findings
}
